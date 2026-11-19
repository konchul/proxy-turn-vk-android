package com.wdtt.client.ui

import android.content.ClipData
import android.content.ActivityNotFoundException
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Color
import android.net.Uri
import android.util.Base64
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.core.graphics.drawable.toBitmap
import android.widget.FrameLayout
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.lifecycleScope
import androidx.webkit.WebViewAssetLoader
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import com.wdtt.client.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.merge
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.text.Collator
import java.util.Locale
import java.util.UUID
import java.util.zip.ZipEntry
import java.util.zip.ZipInputStream
import java.util.zip.ZipOutputStream

internal class ContourApp(private val activity: MainActivity) {
    private val settings = SettingsStore(activity)
    private val profiles = ProfilesStore(activity)
    private val servers = ServersStore(activity)
    private val writes = Mutex()
    private val scope get() = activity.lifecycleScope
    private lateinit var view: WebView
    private var ready = false
    private var pendingOpen: String? = null
    private var importBypass = false
    private var exportBytes: ByteArray? = null
    private var adminEntries = emptyList<AdminApiClient.AdminPassword>()
    private var adminServer = ""

    private val openDocument = activity.registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) importUri(uri, importBypass)
    }
    private val saveDocument = activity.registerForActivityResult(ActivityResultContracts.CreateDocument("application/octet-stream")) { uri ->
        val bytes = exportBytes
        exportBytes = null
        if (uri != null && bytes != null) scope.launch {
            try {
                withContext(Dispatchers.IO) {
                    activity.contentResolver.openOutputStream(uri)?.use { it.write(bytes) }
                        ?: error("Не удалось открыть файл")
                }
                message("Файл сохранён")
            } catch (_: Exception) { message("Не удалось сохранить файл") }
        }
    }

    fun mount() {
        view = WebView(activity)
        view.setBackgroundColor(Color.rgb(11, 11, 12))
        val layout = FrameLayout(activity).apply {
            setBackgroundColor(Color.rgb(11, 11, 12))
            addView(view, FrameLayout.LayoutParams(-1, -1))
        }
        ViewCompat.setOnApplyWindowInsetsListener(layout) { host, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            val keyboard = insets.getInsets(WindowInsetsCompat.Type.ime())
            host.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, keyboard.bottom))
            insets
        }
        WindowCompat.getInsetsController(activity.window, view).apply {
            isAppearanceLightStatusBars = false
            isAppearanceLightNavigationBars = false
        }
        val assets = WebViewAssetLoader.Builder()
            .addPathHandler("/assets/", WebViewAssetLoader.AssetsPathHandler(activity))
            .build()
        val origin = "https://appassets.androidplatform.net"
        val entry = "$origin/assets/contour/index.html"
        val assetRoot = "$origin/assets/contour/"
        val localPages = setOf(entry, "${assetRoot}style.css", "${assetRoot}app.js", "${assetRoot}en.js")
        view.settings.apply {
            javaScriptEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            javaScriptCanOpenWindowsAutomatically = false
            setSupportMultipleWindows(false)
            mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW
        }
        view.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean =
                !request.isForMainFrame || request.url.toString() != entry

            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse {
                val url = request.url.toString()
                val file = url.removePrefix(assetRoot)
                val response = when {
                    url in localPages -> assets.shouldInterceptRequest(request.url)
                    !url.startsWith(assetRoot) -> null
                    file.matches(Regex("flags/[a-z]{2}\\.png")) -> assets.shouldInterceptRequest(request.url)
                    file.matches(Regex("icons/[a-zA-Z0-9_.]+\\.png")) ->
                        installedAppIcon(file.removePrefix("icons/").removeSuffix(".png"))
                    else -> null
                }
                return response ?: WebResourceResponse(
                    "text/plain", "UTF-8", 404, "Not found", emptyMap(), ByteArrayInputStream(byteArrayOf())
                )
            }

            override fun onPageFinished(view: WebView, url: String) {
                if (url != entry) return
                ready = true
                emitTunnel()
                pendingOpen?.let { send("qwdttOpen", it) }
                pendingOpen = null
            }
        }
        view.webChromeClient = WebChromeClient()
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(view, "ContourNative", setOf(origin)) { _, msg, source, mainFrame, _ ->
                if (mainFrame && source.toString().trimEnd('/') == origin) receive(msg.data)
            }
        } else {
            // The only navigable page and all subresources are packaged assets.
            view.addJavascriptInterface(object {
                @android.webkit.JavascriptInterface
                fun postMessage(json: String) = view.post { if (view.url == entry) receive(json) }
            }, "ContourNative")
        }
        activity.setContentView(layout)
        activity.onBackPressedDispatcher.addCallback(activity, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() { if (ready) view.evaluateJavascript("window.qwdttBack?.()", null) }
        })
        view.loadUrl(entry)
        scope.launch {
            merge(TunnelManager.running, TunnelManager.isConnecting, TunnelManager.isReconnecting,
                TunnelManager.connectionPipeline, TunnelManager.stats, TunnelManager.activeWorkers,
                TunnelManager.connectedSinceMs, TunnelManager.lastFatalError, TunnelManager.logs
            ).collect { emitTunnel() }
        }
        scope.launch { TunnelManager.openAppSettingsRequest.collect { if (it > 0) open("settings") } }
        scope.launch {
            SettingsStore.awaitMigrations(activity)
            servers.migrateLegacyServerIfNeeded()
            if (!TunnelManager.running.value) {
                val interval = settings.subscriptionAutoRefreshHours.first()
                if (interval != SettingsStore.SUB_AUTO_REFRESH_NEVER) {
                    runCatching { profiles.autoRefreshSubscriptionsIfDue(interval) }
                }
            }
        }
    }

    fun destroy() {
        ready = false
        if (::view.isInitialized) view.destroy()
    }

    private fun open(page: String) { if (ready) send("qwdttOpen", page) else pendingOpen = page }

    private fun message(text: String) { send("qwdttMessage", text) }

    private fun send(name: String, value: Any) {
        // The local script can request its first snapshot before onPageFinished.
        // It has already registered the response handler at that point.
        if (!ready && name != "qwdttResponse") return
        val json = if (value is String) JSONObject.quote(value) else value.toString()
        view.evaluateJavascript("window.$name?.($json)", null)
    }

    private fun receive(raw: String?) {
        if (raw == null || raw.length > 1_200_000) return
        val request = runCatching { JSONObject(raw) }.getOrNull() ?: return
        val id = request.optLong("id", -1L)
        if (id < 0) return
        val action = request.optString("action")
        val args = request.optJSONObject("data") ?: JSONObject()
        scope.launch {
            val reply = JSONObject().put("id", id)
            try {
                val result = if (action == "tunnel.ping") {
                    check(tunnelReady()) { "Сначала подключите туннель" }
                    TunnelManager.measureLivePing()
                } else writes.withLock { dispatch(action, args, id) }
                reply.put("result", result ?: JSONObject.NULL)
            } catch (cancelled: CancellationException) {
                throw cancelled
            } catch (exception: Exception) {
                reply.put("error", exception.message?.take(300) ?: "Не удалось выполнить действие")
            }
            send("qwdttResponse", reply)
        }
    }

    private fun emitTunnel() {
        val pipeline = TunnelManager.connectionPipeline.value
        val starting = TunnelManager.isConnecting.value || TunnelManager.isReconnecting.value
        val up = TunnelManager.running.value
        val error = TunnelManager.lastFatalError.value
        val finishing = pipeline.current != null && pipeline.current != ConnectionStep.DONE
        val status = when {
            starting -> "starting"
            up && finishing -> "starting"
            up -> "up"
            pipeline.failed != null || error != null -> "failed"
            else -> "down"
        }
        send("qwdttTunnel", obj(
            "link" to status, "phases" to JSONArray(if (pipeline.current != null || pipeline.failed != null) pipeline.stepsToShow().map { it.label } else emptyList()),
            "phase" to pipeline.current?.label, "failed" to pipeline.failed?.label,
            "timeout" to pipeline.timeoutSec.takeIf { status == "failed" && pipeline.timedOut },
            "completed" to JSONArray(pipeline.completed.map { it.label }),
            "workers" to TunnelManager.activeWorkers.value,
            "session" to TunnelManager.connectedSinceMs.value,
            "stats" to TunnelManager.stats.value, "error" to if (status == "failed") error else null,
            "logs" to array(TunnelManager.logs.value.take(100)) { obj("text" to it.message, "count" to it.count, "error" to it.isError) },
        ))
    }

    private suspend fun snapshot(): JSONObject {
        SettingsStore.awaitMigrations(activity)
        val active = settings.currentProfileId.first()
        emitTunnel()
        return obj(
            "active" to active, "role" to settings.interfaceRole.first(), "mode" to settings.connectionMode.first(),
            "aes" to settings.rawAesMode.first(), "tcp" to settings.turnTcpEnabled.first(),
            "mask" to settings.obfsMode.first(), "noDtls" to settings.noDtlsEnabled.first(),
            "verbose" to settings.detailedLogs.first(), "wifi" to settings.stopOnWifi.first(),
            "lang" to settings.contourLanguage.first(), "version" to BuildConfig.VERSION_NAME,
            "autoRoutes" to settings.bypassAutoRefresh.first(), "whitelist" to settings.isWhitelist.first(),
            "routes" to settings.bypassRoutes.first(), "socksPort" to settings.socksPort.first(), "socksAuth" to settings.socksAuthEnabled.first(),
            "dns" to obj("key" to settings.goDnsPreset.first(), "ip" to settings.goDnsCustom.first(), "url" to settings.goDnsDohCustom.first()),
            "profiles" to array(profiles.profiles.first()) {
                obj("id" to it.id, "name" to it.name, "flag" to it.countryFlag, "groupId" to it.groupId,
                    "ping" to PingHelper.pingResults[it.id]?.takeIf { ms -> ms >= 0 })
            },
            "groups" to array(profiles.groups.first()) { obj("id" to it.id, "name" to it.name) },
            "subscriptions" to array(profiles.subscriptions.first()) { obj("id" to it.id, "name" to it.name) },
            "servers" to array(servers.servers.first()) { obj("id" to it.id, "name" to it.name, "host" to it.ip) },
        )
    }

    private suspend fun dispatch(action: String, args: JSONObject, requestId: Long): Any? {
        when (action) {
            "snapshot" -> return snapshot()
            "toggle" -> return toggle()
            "setting" -> setSetting(args)
            "profile.select" -> {
                val id = args.getString("id")
                if (id != settings.currentProfileId.first()) {
                    if (tunnelBusy()) TunnelControl.stop(activity)
                    profiles.applyProfile(activity, profile(id).id)
                }
            }
            "profile.edit" -> return editor(args.optString("id"))
            "profile.save" -> saveProfile(args)
            "profile.delete" -> {
                val id = profile(args.getString("id")).id
                if (id == settings.currentProfileId.first() && tunnelBusy()) TunnelControl.stop(activity)
                profiles.deleteProfile(id)
            }
            "profile.ping" -> {
                check(!tunnelBusy()) { "Остановите туннель перед замером" }
                val p = profile(args.getString("id"))
                val result = PingHelper.measurePing(activity, p.copy(vkHashes = effectiveHashes(p)))
                check(result >= 0) { "Не удалось измерить задержку" }
                PingHelper.pingResults[p.id] = result
            }
            "profile.export" -> return exportProfile(profile(args.getString("id")))
            "profile.import" -> importText(args.getString("raw"))
            "import.file", "bypass-import" -> {
                importBypass = action == "bypass-import"
                openDocument.launch(arrayOf("*/*"))
            }
            "import.clip" -> importText(clipboard().primaryClip?.getItemAt(0)?.coerceToText(activity)?.toString().orEmpty())
            "import.qr" -> GmsBarcodeScanning.getClient(activity).startScan()
                .addOnSuccessListener { barcode ->
                    scope.launch {
                        try { importText(barcode.rawValue.orEmpty()); open("profiles") }
                        catch (exception: Exception) { message(exception.message ?: "Ошибка импорта") }
                    }
                }.addOnFailureListener { message("Не удалось открыть сканер QR") }
            "subscription.add" -> profiles.addSubscription(args.getString("url")).getOrThrow()
            "subscription.refresh" -> profiles.refreshSubscription(args.getString("id")).getOrThrow()
            "folder.create" -> profiles.saveGroup(ProfileGroup(UUID.randomUUID().toString(), args.required("name")))
            "ports.read" -> return readPorts()
            "ports.save" -> savePorts(args)
            "socks.read" -> return obj("user" to settings.socksUsername.first(), "password" to settings.socksPassword.first())
            "socks.save" -> {
                check(!tunnelBusy()) { "Остановите туннель" }
                settings.saveSocksPort(args.port("port"))
                settings.saveSocksAuthEnabled(args.optBoolean("auth"))
                settings.saveSocksUsername(args.optString("user"))
                settings.saveSocksPassword(args.optString("password"))
            }
            "dns.save" -> {
                val key = dnsKey(args)
                settings.saveGoDns(key, args.optString("ip"), args.optString("url"))
            }
            "dns.test" -> {
                val checked = GoDnsProbe.checkPreset(dnsKey(args), args.optString("ip"), args.optString("url"))
                return (if (checked.reachable) "Доступен: " else "Недоступен: ") + checked.statusText
            }
            "hash.check" -> {
                check(!tunnelBusy()) { "Остановите туннель перед проверкой" }
                val slots = strings(args.optJSONArray("hashes")).take(4).map(::stripVkUrlStatic)
                val items = slots.mapIndexedNotNull { i, hash -> if (hash.length >= 16) (i + 1) to hash else null }
                val invalid = slots.mapIndexedNotNull { i, hash ->
                    if (hash.isNotBlank() && hash.length < 16) i + 1 to "invalid" else null
                }.toMap()
                if (items.isEmpty()) return array(invalid.entries.toList()) { (slot, status) ->
                    obj("slot" to slot, "status" to status)
                }
                val result = HashCheckHelper.checkHashes(activity, items, settings.captchaMode.first(),
                    args.optString("vk", "vkcalls"), settings.resolveGoDnsArg()) { slot, status ->
                    scope.launch { send("qwdttHashCheck", obj("id" to requestId, "slot" to slot, "status" to status.status)) }
                }.mapValues { (_, value) -> value.status } + invalid
                return array(result.entries.toList()) { (slot, status) -> obj("slot" to slot, "status" to status) }
            }
            "bypass.routes" -> settings.saveBypassRoutes(BypassRoutes.limitRules(BypassRoutes.parseRules(args.getString("routes"))).joinToString("\n"))
            "bypass.apps" -> return installedApps()
            "bypass.select" -> settings.saveExceptionsMode(strings(args.optJSONArray("apps")).joinToString(","), settings.isWhitelist.first())
            "export.file" -> exportFile(args)
            "copy" -> clipboard().setPrimaryClip(ClipData.newPlainText("qWDTT", args.getString("text")))
            "logs.copy" -> {
                clipboard().setPrimaryClip(ClipData.newPlainText("qWDTT", logsText()))
                return "Скопировано"
            }
            "logs.share" -> share(logsText())
            "logs.clear" -> TunnelManager.clearLogs()
            "notifications" -> activity.openNotificationSettings()
            "github" -> {
                try {
                    activity.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://github.com/konchul/proxy-turn-vk-android")))
                } catch (_: ActivityNotFoundException) {
                    error("Не удалось открыть GitHub")
                }
            }
            "vk.login" -> {
                VkAuthWebViewManager.loginOnly(activity)
                return if (VkAuthWebViewManager.hasVkSessionCookie()) "Вход в VK выполнен" else "Вход в VK не завершён"
            }
            "update" -> {
                val release = fetchLatestReleaseInfo(BuildConfig.VERSION_NAME, settings.includeBetaUpdates.first())
                    ?: error("Не удалось проверить обновления")
                return if (isNewerVersion(BuildConfig.VERSION_NAME, release.versionTag)) "Доступно ${release.versionTag}" else "Установлена последняя версия"
            }
            "server.read" -> return serverJson(server(args))
            "server.save" -> saveServer(args)
            "server.remove" -> servers.deleteServer(server(args).id)
            "server.deploy" -> {
                check(!DeployManager.isDeploying.value) { "Установка уже выполняется" }
                val selected = server(args)
                DeployManager.scope.launch {
                    performMultiDeploy(activity.applicationContext, listOf(selected), {}, { _, outcome ->
                        scope.launch { message(if (outcome is DeployOutcome.Success) "Установка завершена" else "Не удалось установить сервер") }
                    })
                }
                return "Установка запущена"
            }
            "server.access" -> return accessList(server(args))
            "access.save" -> return saveAccess(args)
            "access.toggle" -> return toggleAccess(args)
            "access.share" -> share(adminItem(server(args), args).password)
            else -> error("Неизвестное действие")
        }
        return null
    }

    private suspend fun toggle(): JSONObject {
        if (tunnelBusy()) { TunnelControl.stop(activity); return obj("logs" to false) }
        check(TunnelManager.cooldownSeconds.value == 0) { "Подождите несколько секунд" }
        val selected = settings.currentProfileId.first()
        if (selected.isBlank() && settings.peer.first().isBlank()) error("Выберите профиль")
        check(settings.connectionPassword.first().isNotBlank()) { "Укажите пароль в настройках профиля" }
        check(hashes(settings.vkHashes.first()).isNotEmpty()) { "Добавьте VK-хеши в настройках профиля" }
        val mode = settings.connectionMode.first()
        if (mode == SettingsStore.CONNECTION_MODE_SOCKS && settings.socksAuthEnabled.first()) {
            check(settings.socksUsername.first().isNotBlank() && settings.socksPassword.first().isNotBlank()) { "Укажите логин и пароль SOCKS5" }
        }
        val proceed = {
            TunnelManager.beginConnecting()
            TunnelControl.startFromSavedSettings(activity)
        }
        if (mode == SettingsStore.CONNECTION_MODE_SOCKS) proceed() else activity.prepareVpnThen(proceed)
        return obj("logs" to false)
    }

    private suspend fun setSetting(args: JSONObject) {
        val key = args.getString("key")
        if (key in setOf("mode", "aes", "tcp", "mask")) check(!tunnelBusy()) { "Остановите туннель перед сменой режима" }
        when (key) {
            "mode" -> settings.saveConnectionMode(args.getString("value"))
            "aes" -> settings.saveRawAesMode(args.getString("value") == "1")
            "tcp" -> settings.saveTurnTcpEnabled(args.getString("value") == "1")
            "mask" -> settings.saveObfsMode(args.getString("value"))
            "verbose" -> settings.saveDetailedLogs(args.getBoolean("value"))
            "wifi" -> settings.saveStopOnWifi(args.getBoolean("value"))
            "lang" -> settings.saveContourLanguage(args.getString("value"))
            "autoRoutes" -> settings.saveBypassAutoRefresh(args.getBoolean("value"))
            "whitelist" -> settings.saveExceptionsMode(args.getBoolean("value"))
            "role" -> settings.saveInterfaceRole(args.getString("value"))
            else -> error("Настройка не найдена")
        }
    }

    private suspend fun profile(id: String): ConnectionProfile = profiles.getProfileOnce(id) ?: error("Профиль не найден")

    private suspend fun editor(id: String): JSONObject {
        val p = if (id.isBlank()) null else profile(id)
        val display = Locale.forLanguageTag(settings.contourLanguage.first())
        val order = Collator.getInstance(display)
        val countries = Locale.getISOCountries().map { it to Locale("", it).getDisplayCountry(display) }
            .sortedWith { a, b -> order.compare(a.second, b.second) }
        return obj(
            "id" to id, "name" to p?.name.orEmpty(), "peer" to PeerAddress.host(p?.peer.orEmpty()),
            "hashes" to JSONArray(hashes(if (p == null) settings.globalVkHashes.first() else effectiveHashes(p))),
            "password" to p?.password.orEmpty(), "flag" to p?.countryFlag.orEmpty(),
            "countries" to array(countries) { (code, name) -> obj("code" to code, "name" to name) },
            "workers" to (p?.workersPerHash ?: 9),
            "groupId" to p?.groupId.orEmpty(), "vk" to settings.vkAnonPath.first(),
            "account" to (settings.vkAuthMode.first() == "account"),
        )
    }

    private suspend fun saveProfile(args: JSONObject) {
        val id = args.optString("id")
        val prior = if (id.isBlank()) null else profile(id)
        val profileHashes = hashes(strings(args.optJSONArray("hashes")).joinToString(","))
        val max = SettingsStore.maxAnonymousWorkers(profileHashes.size)
        val requested = args.optInt("workers", 9)
        val workers = ((requested.coerceIn(9, max) + 4) / 9) * 9
        val name = args.required("name")
        val address = args.required("peer")
        val port = prior?.peer?.let(PeerAddress::port)
        val peer = if (port == null) address else PeerAddress.ensurePort(address, port)
        val flag = args.optString("flag", prior?.countryFlag.orEmpty()).uppercase()
        val entry = (prior ?: ConnectionProfile(UUID.randomUUID().toString(), name, peer, "", 9, 9000, "")).copy(
            name = name, peer = peer, vkHashes = profileHashes.joinToString(","), workersPerHash = workers,
            password = args.optString("password", prior?.password.orEmpty()),
            countryFlag = flag.takeIf { it.matches(Regex("[A-Z]{2}")) }.orEmpty(),
            groupId = args.optString("groupId"), useGlobalHashes = false,
        )
        settings.saveVkAnonPath(args.optString("vk", "vkcalls"))
        settings.saveVkAuthMode(if (args.optBoolean("account")) "account" else "anonymous")
        profiles.saveProfile(entry)
        if (settings.currentProfileId.first() == entry.id) profiles.applyProfile(activity, entry.id)
    }

    private suspend fun readPorts(): JSONObject =
        obj("dtls" to settings.serverDtlsPort.first(), "wg" to settings.serverWgPort.first(),
            "local" to settings.listenPort.first())

    private suspend fun savePorts(args: JSONObject) {
        val dtls = args.port("dtls")
        val wg = args.port("wg")
        val local = args.port("local")
        settings.savePorts(dtls, wg, local)
        settings.saveManualPortsEnabled(dtls != 56000 || wg != 56001 || local != 9000)
        profiles.getProfileOnce(settings.currentProfileId.first())?.let { old ->
            profiles.saveProfile(old.copy(listenPort = local))
        }
    }

    private fun dnsKey(args: JSONObject): String {
        val key = SettingsStore.normalizeGoDnsPreset(args.optString("key"))
        if (key == "custom") require(SettingsStore.normalizeGoDnsServers(args.optString("ip")).isNotBlank()) { "Укажите IP DNS" }
        if (key == "doh-custom") require(SettingsStore.normalizeGoDnsDohUrls(args.optString("url")).isNotBlank()) { "Укажите HTTPS URL DoH" }
        return key
    }

    private suspend fun effectiveHashes(p: ConnectionProfile): String =
        if (p.useGlobalHashes) settings.globalVkHashes.first().ifEmpty { p.vkHashes } else p.vkHashes

    private suspend fun exportProfile(p: ConnectionProfile): JSONObject {
        val url = profileLink(p)
        val bitmap = withContext(Dispatchers.Default) { generateQrCode(url, 480) } ?: error("Не удалось создать QR")
        val bytes = ByteArrayOutputStream().use { out -> bitmap.compress(Bitmap.CompressFormat.PNG, 100, out); out.toByteArray() }
        return obj("id" to p.id, "link" to url, "qr" to "data:image/png;base64," + Base64.encodeToString(bytes, Base64.NO_WRAP))
    }

    private suspend fun profileLink(p: ConnectionProfile): String = Uri.Builder().scheme("qwdtt").authority("config")
        .appendQueryParameter("name", p.name)
        .apply { if (p.countryFlag.isNotEmpty()) appendQueryParameter("flag", p.countryFlag) }
        .appendQueryParameter("peer", PeerAddress.ensurePort(p.peer, settings.serverDtlsPort.first()))
        .appendQueryParameter("hashes", effectiveHashes(p))
        .appendQueryParameter("workers", p.workersPerHash.toString())
        .appendQueryParameter("port", p.listenPort.toString())
        .appendQueryParameter("pass", p.password).build().toString()

    private suspend fun profileFile(p: ConnectionProfile) = obj(
        "name" to p.name, "peer" to PeerAddress.ensurePort(p.peer, settings.serverDtlsPort.first()),
        "flag" to p.countryFlag,
        "vkHashes" to effectiveHashes(p), "workersPerHash" to p.workersPerHash, "listenPort" to p.listenPort, "password" to p.password,
    ).toString(2)

    private suspend fun exportFile(args: JSONObject) {
        val name = when (args.getString("kind")) {
            "profile" -> {
                exportBytes = profileFile(profile(args.getString("id"))).toByteArray()
                "qwdtt-profile.qwdtt"
            }
            "bypass" -> {
                exportBytes = BypassShare.exportJson(settings).toByteArray()
                "qwdtt-bypass.json"
            }
            "zip" -> {
                val buffer = ByteArrayOutputStream()
                ZipOutputStream(buffer).use { zip ->
                    profiles.profiles.first().forEachIndexed { index, item ->
                        zip.putNextEntry(ZipEntry("profile-${index + 1}.qwdtt"))
                        zip.write(profileFile(item).toByteArray())
                        zip.closeEntry()
                    }
                }
                exportBytes = buffer.toByteArray()
                "qwdtt-profiles.zip"
            }
            else -> error("Формат не поддерживается")
        }
        saveDocument.launch(name)
    }

    fun importUri(uri: Uri, bypass: Boolean = false) {
        scope.launch {
            try {
                writes.withLock {
                    val raw = uri.toString()
                    if (SubscriptionImport.isConfigLink(raw) || uri.scheme == "wdtt") importText(raw)
                    else {
                        val bytes = withContext(Dispatchers.IO) {
                            activity.contentResolver.openInputStream(uri)?.use { readLimited(it, 4_194_304) }
                                ?: error("Не удалось прочитать файл")
                        }
                        if (bypass) BypassShare.importJson(settings, bytes.decodeToString())
                        else if (bytes.size >= 4 && bytes[0] == 0x50.toByte() && bytes[1] == 0x4b.toByte()) importZip(bytes)
                        else importText(bytes.decodeToString())
                    }
                }
                open(if (bypass) "bypass" else "profiles")
                message("Импорт завершён")
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (exception: Exception) { message(exception.message ?: "Не удалось импортировать файл") }
        }
    }

    private suspend fun importZip(bytes: ByteArray) {
        val items = withContext(Dispatchers.IO) {
            buildList {
                var size = 0
                ZipInputStream(ByteArrayInputStream(bytes)).use { zip ->
                    repeat(200) {
                        val entry = zip.nextEntry ?: return@use
                        if (!entry.isDirectory) {
                            val payload = readLimited(zip, 1_048_576)
                            size += payload.size
                            require(size <= 8_388_608) { "Слишком большой архив" }
                            add(payload.decodeToString())
                        }
                        zip.closeEntry()
                    }
                }
            }
        }
        val entries = items.flatMap(::parseProfiles)
        require(entries.isNotEmpty()) { "В архиве нет профилей" }
        for (entry in entries) profiles.saveProfile(entry)
    }

    private suspend fun importText(raw: String) {
        val entries = parseProfiles(raw)
        require(entries.isNotEmpty()) { "Не удалось прочитать профиль" }
        for (entry in entries) profiles.saveProfile(entry)
    }

    private fun parseProfiles(raw: String): List<ConnectionProfile> {
        val normalized = raw.trim().removePrefix("\uFEFF")
        return SubscriptionImport.parsePayload(normalized)?.profiles?.takeIf { it.isNotEmpty() }
            ?: listOfNotNull(parseQrConfig(normalized))
    }

    private suspend fun installedApps(): JSONArray {
        val selected = settings.excludedApps.first().split(",").toSet()
        return withContext(Dispatchers.IO) {
            val pm = activity.packageManager
            val launch = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
            val appList = pm.queryIntentActivities(launch, 0).mapNotNull { it.activityInfo?.applicationInfo }
                .distinctBy { it.packageName }.sortedBy { pm.getApplicationLabel(it).toString().lowercase() }
            array(appList) { obj("id" to it.packageName, "label" to pm.getApplicationLabel(it).toString(), "selected" to (it.packageName in selected)) }
        }
    }

    private fun installedAppIcon(packageName: String): WebResourceResponse? = runCatching {
        val icon = activity.packageManager.getApplicationIcon(packageName).toBitmap(48, 48)
        val bytes = ByteArrayOutputStream().use { out ->
            icon.compress(Bitmap.CompressFormat.PNG, 100, out)
            out.toByteArray()
        }
        WebResourceResponse("image/png", null, ByteArrayInputStream(bytes))
    }.getOrNull()

    private suspend fun server(args: JSONObject): ManagedServer {
        check(settings.interfaceRole.first() == "admin") { "Откройте режим администратора" }
        return servers.getServerOnce(args.optString("id")) ?: error("Сервер не найден")
    }

    private fun serverJson(s: ManagedServer) = obj("id" to s.id, "name" to s.name, "host" to s.ip,
        "login" to s.sshLogin, "port" to s.sshPort, "password" to s.sshPassword, "adminPassword" to s.adminPassword,
        "useKey" to s.sshUseKey, "keyName" to s.sshKeyName, "dtls" to s.dtlsPort, "wg" to s.wgPort)

    private suspend fun saveServer(args: JSONObject) {
        check(settings.interfaceRole.first() == "admin") { "Откройте режим администратора" }
        val id = args.optString("id")
        val host = args.required("host")
        val name = args.required("name")
        val existing = if (id.isBlank()) null else server(args)
        val privateKey = args.optString("privateKey").trim().ifEmpty { existing?.sshPrivateKey.orEmpty() }
        val useKey = args.optBoolean("useKey")
        require(!useKey || privateKey.isNotBlank()) { "Укажите приватный SSH-ключ" }
        val dtls = args.port("dtls")
        val wg = args.port("wg")
        val entry = (existing ?: ManagedServer(UUID.randomUUID().toString(), name, host, "root", "", false, "", "", "",
            "22", "1.1.1.1", "1.0.0.1", "")).copy(
            name = name, ip = host, sshLogin = args.required("login"), sshPort = args.port("port").toString(),
            sshPassword = args.optString("password"), adminPassword = args.optString("adminPassword"),
            sshUseKey = useKey, sshPrivateKey = privateKey,
            sshKeyPassphrase = args.optString("keyPassphrase").ifEmpty { existing?.sshKeyPassphrase.orEmpty() },
            dtlsPort = dtls, wgPort = wg, manualPortsEnabled = dtls != 56000 || wg != 56001,
        )
        if (existing == null) servers.addServer(entry) else servers.updateServer(entry)
    }

    private suspend fun accessList(server: ManagedServer): JSONArray {
        AdminApiClient.configureServer(server.ip, server.adminCertPin)
        adminEntries = AdminApiClient.listPasswords(server.ip, server.dtlsPort, server.adminApiToken.ifBlank { server.adminPassword }).requireSuccess()
        adminServer = server.id
        return array(adminEntries.withIndex().toList()) { (index, item) ->
            obj("index" to index, "name" to item.label.ifBlank { "Доступ ${index + 1}" }, "devices" to item.activeDevices,
                "active" to !item.isDeactivated, "max" to item.maxDevices)
        }
    }

    private fun adminItem(server: ManagedServer, args: JSONObject): AdminApiClient.AdminPassword {
        check(adminServer == server.id) { "Обновите список доступов" }
        return adminEntries.getOrNull(args.getInt("index")) ?: error("Доступ не найден")
    }

    private suspend fun saveAccess(args: JSONObject): JSONArray {
        val s = server(args)
        val token = s.adminApiToken.ifBlank { s.adminPassword }
        val name = args.required("name")
        val max = args.optInt("max", 1).coerceAtLeast(1)
        val days = args.optString("days").toIntOrNull()
        if (args.has("index") && !args.isNull("index")) {
            AdminApiClient.updatePassword(s.ip, s.dtlsPort, token, adminItem(s, args).password, label = name, maxDevices = max, days = days).requireSuccess()
        } else {
            AdminApiClient.createPassword(s.ip, s.dtlsPort, token, args.optString("hash"), days ?: 30, max, name).requireSuccess()
        }
        return accessList(s)
    }

    private suspend fun toggleAccess(args: JSONObject): JSONArray {
        val s = server(args)
        val token = s.adminApiToken.ifBlank { s.adminPassword }
        val pass = adminItem(s, args).password
        if (args.optBoolean("active")) AdminApiClient.activatePassword(s.ip, s.dtlsPort, token, pass).requireSuccess()
        else AdminApiClient.deactivatePassword(s.ip, s.dtlsPort, token, pass).requireSuccess()
        return accessList(s)
    }

    private fun <T> AdminApiClient.Result<T>.requireSuccess(): T = when (this) {
        is AdminApiClient.Result.Success -> value
        is AdminApiClient.Result.Failure -> error(message)
    }

    private fun logsText() = (listOf(TunnelManager.stats.value) + TunnelManager.logs.value.map { it.message }).joinToString("\n")
    private fun clipboard() = activity.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    private fun share(text: String) {
        activity.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text), "Поделиться"))
    }
    private fun tunnelBusy() = TunnelManager.running.value || TunnelManager.isConnecting.value || TunnelManager.isReconnecting.value
    private fun tunnelReady() = TunnelManager.running.value && !TunnelManager.isReconnecting.value &&
        TunnelManager.connectionPipeline.value.current == ConnectionStep.DONE
    private fun hashes(raw: String) = raw.split(Regex("[,\\s]+")).map(::stripVkUrlStatic).filter { it.length >= 16 }.distinct().take(4)
    private fun JSONObject.required(key: String) = optString(key).trim().also { require(it.isNotEmpty()) { "Заполните обязательные поля" } }
    private fun JSONObject.port(key: String) = optString(key).toIntOrNull()?.takeIf { it in 1..65535 } ?: error("Порт должен быть от 1 до 65535")
    private fun strings(values: JSONArray?) = if (values == null) emptyList() else List(values.length()) { values.optString(it) }
    private fun obj(vararg pairs: Pair<String, Any?>) = JSONObject().apply { for ((key, value) in pairs) put(key, value ?: JSONObject.NULL) }
    private fun <T> array(items: List<T>, map: (T) -> JSONObject) = JSONArray().apply { for (item in items) put(map(item)) }
    private fun readLimited(stream: java.io.InputStream, limit: Int): ByteArray {
        val out = ByteArrayOutputStream()
        val buffer = ByteArray(8192)
        while (true) {
            val read = stream.read(buffer)
            if (read < 0) break
            require(out.size() + read <= limit) { "Файл слишком большой" }
            out.write(buffer, 0, read)
        }
        return out.toByteArray()
    }
}
