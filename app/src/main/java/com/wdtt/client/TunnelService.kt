package com.wdtt.client

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.LinkProperties
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.collectLatest

private const val TUNNEL_NOTIFICATION_CHANNEL_ID = NotificationHelper.TUNNEL_CHANNEL_ID
private const val TUNNEL_NOTIFICATION_ID = 1

class TunnelService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var updateJob: Job? = null
    private var lastNotificationText: String? = null
    
    // Network Monitoring
    private var connectivityManager: ConnectivityManager? = null
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private var lastNetworkChangeTime = 0L
    private val activeNetworks = mutableSetOf<Network>()
    private val networkFingerprints = mutableMapOf<Network, String>()
    private var lastUnderlyingFingerprint = ""
    private var isTunnelPaused = false
    private var lastVpnReconnectAttemptMs = 0L
    private var wasOnWifi = false

    override fun onCreate() {
        super.onCreate()
        NotificationHelper.ensureTunnelChannel(this)
        createNotificationChannel()
        // Сразу берем лок при создании
        acquireWakeLock()
        setupNetworkCallback()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent == null) {
            restoreTunnel()
            return START_STICKY
        }

        when (intent.action) {
            "START", "START_FORCED" -> {
                val notification = createNotification("Запуск...")
                startPersistentForeground(notification)

                val appContext = applicationContext
                TunnelManager.scope.launch {
                    try {
                        val store = SettingsStore(appContext)
                        SettingsStore.awaitMigrations(appContext)
                        val basePeer = intent.getStringExtra("peer")?.takeIf { it.isNotEmpty() } ?: store.peer.first()
                        val manualPortsEnabled = store.manualPortsEnabled.first()
                        val serverDtlsPort = if (manualPortsEnabled) store.serverDtlsPort.first() else 56000
                        val peerWithPort = if (basePeer.isBlank()) basePeer else PeerAddress.ensurePort(basePeer, serverDtlsPort)
                        val vkAnonPath = SettingsStore.normalizeVkAnonPath(
                            intent.getStringExtra("vk_anon_path")?.takeIf { it.isNotEmpty() }
                                ?: store.vkAnonPath.first()
                        )
                        val goDnsArg = intent.getStringExtra("go_dns_arg")?.takeIf { it.isNotEmpty() }
                            ?: store.resolveGoDnsArg()
                        val obfsMode = SettingsStore.normalizeObfsMode(
                            intent.getStringExtra("obfs_mode")?.takeIf { it.isNotEmpty() }
                                ?: store.obfsMode.first()
                        )
                        
                        val params = TunnelParams(
                            peer = peerWithPort,
                            vkHashes = intent.getStringExtra("vk_hashes")?.takeIf { it.isNotEmpty() } ?: store.vkHashes.first(),
                            secondaryVkHash = intent.getStringExtra("secondary_vk_hash")?.takeIf { it.isNotEmpty() } ?: store.secondaryVkHash.first(),
                            workersPerHash = intent.getIntExtra("workers_per_hash", 0).takeIf { it > 0 } ?: store.workersPerHash.first(),
                            port = intent.getIntExtra("port", 0).takeIf { it > 0 } ?: store.listenPort.first(),
                            sni = intent.getStringExtra("sni")?.takeIf { it.isNotEmpty() } ?: store.sni.first(),
                            connectionPassword = intent.getStringExtra("connection_password")?.takeIf { it.isNotEmpty() } ?: store.connectionPassword.first(),
                            protocol = intent.getStringExtra("protocol")?.takeIf { it.isNotEmpty() } ?: store.protocol.first(),
                            captchaMode = sanitizeCaptchaMode(intent.getStringExtra("captcha_mode")?.takeIf { it.isNotEmpty() } ?: store.captchaMode.first()),
                            captchaSolveMethod = intent.getStringExtra("captcha_solve_method")?.takeIf { it.isNotEmpty() } ?: store.captchaSolveMethod.first(),
                            vkAuthMode = intent.getStringExtra("vk_auth_mode")?.takeIf { it.isNotEmpty() } ?: store.vkAuthMode.first(),
                            vkAnonPath = vkAnonPath,
                            goDnsArg = goDnsArg,
                            obfsMode = obfsMode,
                            detailedLogs = store.detailedLogs.first()
                        )
                        launch(Dispatchers.Main) {
                            startTunnel(params, forceStart = intent.action == "START_FORCED")
                        }
                    } catch (e: kotlinx.coroutines.CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        launch(Dispatchers.Main) { stopTunnel() }
                    }
                }
            }
            "STOP" -> stopTunnel()
            "DEPLOY_START" -> {
                val notification = createNotification("Установка на сервер...", "DEPLOY_CANCEL", "Отменить")
                startPersistentForeground(notification)
                acquireWakeLock()
            }
            "DEPLOY_CANCEL" -> {
                DeployManager.writeError("[!] ❌ Установка отменена пользователем")
                DeployManager.stopDeploy("error: Отменена пользователем")
                stopForeground(STOP_FOREGROUND_REMOVE)
            }
            "DEPLOY_STOP" -> {
                if (!TunnelManager.running.value) {
                    stopTunnel()
                } else {
                    updateNotification("Туннель активен")
                }
            }
        }
        return START_STICKY
    }

    private fun restoreTunnel() {
        val notification = createNotification("Восстановление соединения...")
        startPersistentForeground(notification)
        
        val appContext = applicationContext
        TunnelManager.scope.launch {
            try {
                val store = SettingsStore(appContext)
                SettingsStore.awaitMigrations(appContext)
                val basePeer = store.peer.first()
                val manualPortsEnabled = store.manualPortsEnabled.first()
                val serverDtlsPort = if (manualPortsEnabled) store.serverDtlsPort.first() else 56000
                val peerWithPort = if (basePeer.isBlank()) basePeer else PeerAddress.ensurePort(basePeer, serverDtlsPort)
                val params = TunnelParams(
                    peer = peerWithPort,
                    vkHashes = store.vkHashes.first(),
                    secondaryVkHash = store.secondaryVkHash.first(),
                    workersPerHash = store.workersPerHash.first(),
                    port = store.listenPort.first(),
                    sni = store.sni.first(),
                    connectionPassword = store.connectionPassword.first(),
                    captchaMode = sanitizeCaptchaMode(store.captchaMode.first()),
                    captchaSolveMethod = store.captchaSolveMethod.first(),
                    vkAuthMode = store.vkAuthMode.first(),
                    vkAnonPath = SettingsStore.normalizeVkAnonPath(store.vkAnonPath.first()),
                    goDnsArg = store.resolveGoDnsArg(),
                    obfsMode = SettingsStore.normalizeObfsMode(store.obfsMode.first()),
                    detailedLogs = store.detailedLogs.first()
                )
                if (params.peer.isNotEmpty() && params.vkHashes.isNotEmpty()) {
                    launch(Dispatchers.Main) {
                        startTunnel(params)
                    }
                } else {
                    launch(Dispatchers.Main) {
                        stopTunnel()
                    }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                launch(Dispatchers.Main) {
                    stopTunnel()
                }
            }
        }
    }

    private fun startTunnel(params: TunnelParams, forceStart: Boolean = false) {
        wasOnWifi = isUnderlyingWifiActive()
        updateNotification("Подключение...")
        acquireWakeLock()
        acquireWifiLock()

        // Подготавливаем CaptchaWebViewManager (не создаёт WebView — просто сохраняет контекст)
        // Вызываем всегда — дёшево, а WebView создаётся на лету при каждом запросе капчи
        CaptchaWebViewManager.onTunnelStart(applicationContext)

        TunnelManager.start(this, params, isSwitching = false, forceStart = forceStart)
        startStatsUpdater()
    }

    private fun stopTunnel() {
        updateJob?.cancel()

        // Уничтожаем текущий WebView (если капча решается) и чистим контекст
        CaptchaWebViewManager.onTunnelStop()

        TunnelManager.stop()
        releaseWakeLock()
        releaseWifiLock()
        stopForeground(STOP_FOREGROUND_REMOVE)
        getSystemService(NotificationManager::class.java).cancel(TUNNEL_NOTIFICATION_ID)
        TunnelWidgetProvider.updateWidgetState(applicationContext, false, "Нажмите для подключения")
        QuickToggleTileService.requestTileUpdate(applicationContext)
        AppShortcuts.refreshAsync(applicationContext)
        stopSelf()
    }

    private fun setupNetworkCallback() {
        connectivityManager = getSystemService(CONNECTIVITY_SERVICE) as ConnectivityManager
        activeNetworks.clear()
        
        networkCallback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                super.onAvailable(network)
                val wasEmpty = activeNetworks.isEmpty()
                activeNetworks.add(network)
                rememberNetworkFingerprint(network)
                if (wasEmpty) {
                    if (isTunnelPaused) {
                        isTunnelPaused = false
                        Log.d("TunnelService", "Сеть появилась, возобновляем туннель")
                        updateNotification("Переподключение...")
                        TunnelManager.resume()
                    } else {
                        noteUnderlyingNetworkChange()
                    }
                } else {
                    noteUnderlyingNetworkChange()
                }
            }

            override fun onLost(network: Network) {
                super.onLost(network)
                activeNetworks.remove(network)
                networkFingerprints.remove(network)
                if (activeNetworks.isEmpty() && TunnelManager.running.value && !isTunnelPaused) {
                    isTunnelPaused = true
                    lastUnderlyingFingerprint = ""
                    Log.d("TunnelService", "Сеть потеряна, приостанавливаем туннель")
                    TunnelManager.pause()
                    updateNotification("Ожидание сети...")
                } else {
                    noteUnderlyingNetworkChange()
                }
            }

            override fun onCapabilitiesChanged(network: Network, networkCapabilities: NetworkCapabilities) {
                super.onCapabilitiesChanged(network, networkCapabilities)
                if (network !in activeNetworks) return
                val prev = networkFingerprints[network]
                val next = rememberNetworkFingerprint(network)
                if (prev != null && prev != next) {
                    noteUnderlyingNetworkChange()
                }
            }

            override fun onLinkPropertiesChanged(network: Network, linkProperties: LinkProperties) {
                super.onLinkPropertiesChanged(network, linkProperties)
                if (network !in activeNetworks) return
                noteUnderlyingNetworkChange()
            }
        }

        // ВАЖНО: Слушаем только реальные (не VPN) сети с доступом в интернет.
        // Иначе интерфейс VPN (tun0) считается активной сетью, и при "Режиме полёта" activeNetworks не падает до 0.
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
            
        connectivityManager?.registerNetworkCallback(request, networkCallback!!)
        wasOnWifi = isUnderlyingWifiActive()
    }

    /**
     * Wi‑Fi для stop-on-Wi‑Fi: только валидированная сеть.
     * Иначе на OnePlus Wi‑Fi уже в activeNetworks до переключения трафика.
     */
    private fun isUnderlyingWifiActive(): Boolean {
        val cm = connectivityManager ?: return false
        return activeNetworks.any { network ->
            val caps = cm.getNetworkCapabilities(network) ?: return@any false
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) &&
                caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
                caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        }
    }

    private fun networkCapabilityFingerprint(caps: NetworkCapabilities): String {
        val transports = buildList {
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) add("wifi")
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) add("cell")
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) add("eth")
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) add("vpn")
        }.joinToString("+")
        val validated = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)
        val internet = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
        return "$transports|validated=$validated|internet=$internet"
    }

    private fun rememberNetworkFingerprint(network: Network): String {
        val cm = connectivityManager ?: return ""
        val caps = cm.getNetworkCapabilities(network) ?: return ""
        val fingerprint = networkCapabilityFingerprint(caps)
        networkFingerprints[network] = fingerprint
        return fingerprint
    }

    private fun activeUnderlyingFingerprint(): String {
        return activeNetworks.mapNotNull { network ->
            networkFingerprints[network]?.let { fingerprint -> "$network:$fingerprint" }
        }.sorted().joinToString("|")
    }

    private fun noteUnderlyingNetworkChange() {
        val fingerprint = activeUnderlyingFingerprint()
        if (fingerprint.isEmpty()) return
        if (lastUnderlyingFingerprint.isEmpty()) {
            lastUnderlyingFingerprint = fingerprint
            return
        }
        if (fingerprint == lastUnderlyingFingerprint) return
        lastUnderlyingFingerprint = fingerprint
        handleNetworkChange()
    }
    
    private fun handleNetworkChange() {
        val now = System.currentTimeMillis()
        if (now - lastNetworkChangeTime < 3000) return
        lastNetworkChangeTime = now

        val nowOnWifi = isUnderlyingWifiActive()
        val transitionedToWifi = nowOnWifi && !wasOnWifi
        wasOnWifi = nowOnWifi

        // Только переход «не Wi‑Fi → валидированный Wi‑Fi».
        // Старт уже на Wi‑Fi не гасим — иначе нельзя пользоваться VPN дома.
        if (transitionedToWifi && TunnelManager.running.value && !isTunnelPaused) {
            TunnelManager.scope.launch {
                val stopOnWifi = SettingsStore(applicationContext).stopOnWifi.first()
                if (stopOnWifi) {
                    Log.d("TunnelService", "Подключились к Wi-Fi — отключаем туннель по настройке")
                    TunnelManager.addNetworkLog("[СЕТЬ] Wi‑Fi: туннель отключён (опция «Отключать на Wi‑Fi»)")
                    launch(Dispatchers.Main) { stopTunnel() }
                    return@launch
                }
                restartTransportIfRunning()
            }
            return
        }

        restartTransportIfRunning()
    }

    private fun restartTransportIfRunning() {
        if (TunnelManager.running.value && !isTunnelPaused) {
            Log.d("TunnelService", "Сеть изменилась, переподключение транспорта и VPN")
            updateNotification("Переподключение (смена сети)...")
            TunnelManager.restartTransport()
        }
    }

    private fun sanitizeCaptchaMode(mode: String?): String {
        return when (mode?.lowercase()) {
            "auto" -> "auto"
            "rjs" -> "rjs"
            "wv" -> "wv"
            else -> "auto"
        }
    }


    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) return
        val pm = getSystemService(POWER_SERVICE) as PowerManager
        wakeLock = pm.newWakeLock(
            PowerManager.PARTIAL_WAKE_LOCK,
            "wdtt:tunnel_cpu"
        ).apply { 
            setReferenceCounted(false)
            acquire() 
        }
    }

    @Suppress("DEPRECATION")
    private fun acquireWifiLock() {
        if (wifiLock?.isHeld == true) return
        val wm = applicationContext.getSystemService(WIFI_SERVICE) as WifiManager
        
        // Используем WIFI_MODE_FULL_LOW_LATENCY для Android 10+, 
        // это предотвращает отключение радиомодуля при выключенном экране
        val mode = if (Build.VERSION.SDK_INT >= 29) {
            WifiManager.WIFI_MODE_FULL_LOW_LATENCY
        } else {
            WifiManager.WIFI_MODE_FULL_HIGH_PERF
        }
        
        wifiLock = wm.createWifiLock(mode, "wdtt:wifi_perf").apply { 
            setReferenceCounted(false)
            acquire() 
        }
    }

    private fun releaseWakeLock() {
        if (wakeLock?.isHeld == true) {
            wakeLock?.release()
        }
        wakeLock = null
    }

    private fun releaseWifiLock() {
        if (wifiLock?.isHeld == true) {
            wifiLock?.release()
        }
        wifiLock = null
    }

    private fun startStatsUpdater() {
        updateJob?.cancel()
        updateJob = TunnelManager.scope.launch(Dispatchers.Main) {
            // Сторож следит за пропажей уже поднятого VPN-интерфейса, а не за его
            // отсутствием во время подключения (капча, VK-креды и т.д.).
            var wasEverUp = false
            delay(1000)

            val reconnectObserver = launch {
                TunnelManager.isReconnecting.collectLatest { reconnecting ->
                    if (reconnecting && TunnelManager.running.value && !isTunnelPaused) {
                        updateNotification("Переподключение...")
                    }
                }
            }

            try {
                while (isActive) {
                    if (!TunnelManager.running.value && !isTunnelPaused) {
                        // Туннель полностью остановлен (не на паузе) — убиваем сервис
                        stopSelf()
                        break
                    }
                    if (TunnelManager.running.value && !isTunnelPaused) {
                        val helper = WireGuardHelper(applicationContext)
                        when (helper.watchdogState()) {
                            WireGuardHelper.WatchdogState.UP -> wasEverUp = true
                            WireGuardHelper.WatchdogState.DISABLED_BY_EMPTY_WHITELIST -> Unit
                            WireGuardHelper.WatchdogState.DOWN -> {
                                if (wasEverUp) {
                                    val now = System.currentTimeMillis()
                                    if (now - lastVpnReconnectAttemptMs >= 15_000) {
                                        lastVpnReconnectAttemptMs = now
                                        Log.w(
                                            "TunnelService",
                                            "VPN-интерфейс пропал — пробуем переподключение"
                                        )
                                        updateNotification("Переподключение VPN...")
                                        TunnelManager.reconnectAll("пропал VPN-интерфейс")
                                        wasEverUp = false
                                    }
                                }
                            }
                        }
                    }
                    if (!isTunnelPaused && !TunnelManager.isReconnecting.value) {
                        updateNotification(buildTunnelNotificationText())
                    }
                    delay(2000)
                }
            } finally {
                reconnectObserver.cancel()
            }
        }
    }

    private fun buildTunnelNotificationText(): String {
        val statsText = TunnelManager.stats.value.trim()
        return when {
            statsText.isEmpty() -> "Туннель активен"
            statsText == "Ожидание данных..." -> "Туннель активен"
            else -> statsText
        }
    }

    private fun createNotificationChannel() {
        NotificationHelper.ensureTunnelChannel(this)
    }

    private fun createNotification(text: String, actionName: String = "STOP", actionTitle: String = "Отключить"): Notification {
        val openIntent = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        
        val stopIntent = PendingIntent.getService(
            this, if (actionName == "STOP") 1 else 2,
            Intent(this, TunnelService::class.java).apply { action = actionName },
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        return NotificationCompat.Builder(this, TUNNEL_NOTIFICATION_CHANNEL_ID)
            .setContentTitle("qWDTT")
            .setContentText(text)
            .setSmallIcon(R.drawable.ic_stat_connected)
            .setOngoing(true)
            .setLocalOnly(true)
            .setContentIntent(openIntent)
            .addAction(R.drawable.ic_stop, actionTitle, stopIntent)
            .setForegroundServiceBehavior(NotificationCompat.FOREGROUND_SERVICE_DEFAULT)
            // ВАЖНО: Делаем уведомление публичным (видимым на локскрине)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            // Категория SERVICE помогает системе понять важность
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setShowWhen(true)
            .setUsesChronometer(false)
            .setWhen(System.currentTimeMillis())
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build()
    }

    private fun startPersistentForeground(notification: Notification) {
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(TUNNEL_NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(TUNNEL_NOTIFICATION_ID, notification)
        }
    }

    private fun updateNotification(text: String) {
        if (lastNotificationText == text) return
        lastNotificationText = text
        val notification = createNotification(text)
        // Обновляем через startForeground — так надёжнее на Android 13+ и китайских прошивках.
        startPersistentForeground(notification)
        TunnelWidgetProvider.updateWidgetState(applicationContext, TunnelManager.running.value, text)
        QuickToggleTileService.requestTileUpdate(applicationContext)
        AppShortcuts.refreshAsync(applicationContext)
    }

    override fun onDestroy() {
        super.onDestroy()
        networkCallback?.let {
            connectivityManager?.unregisterNetworkCallback(it)
        }
        stopTunnel()
    }

    override fun onBind(intent: Intent?): IBinder? = null
}
