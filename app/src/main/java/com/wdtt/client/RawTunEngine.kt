package com.wdtt.client

import android.app.ActivityManager
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Управление [RawTunVpnService]: поднимает TUN с параметрами из RAWCONF и
 * передаёт fd в go_client через [TunFdBridge] — без промежуточного netstack,
 * go_client получает сырые IP-пакеты напрямую.
 */
object RawTunEngine {
    private const val TAG = "RawTunEngine"
    private val mutex = Mutex()

    @Volatile
    var running: Boolean = false
        private set

    private fun diag(message: String, isError: Boolean = false) {
        Log.i(TAG, message)
        runCatching { TunnelManager.addRawDiagLog(message, isError) }
    }

    suspend fun start(context: Context, ip: String, dnsCsv: String, mtu: Int, sockName: String) =
        mutex.withLock {
            withContext(Dispatchers.IO) {
                val app = context.applicationContext
                diag("RawTunEngine.start(): ip=$ip mtu=$mtu sockName=$sockName")
                diag("Android: SDK=${Build.VERSION.SDK_INT} manufacturer=${Build.MANUFACTURER} model=${Build.MODEL} fingerprint=${Build.FINGERPRINT}")
                logBatteryOptimizationState(app)

                val prepareResult = VpnService.prepare(app)
                if (prepareResult != null) {
                    diag("VpnService.prepare() != null — permission NOT granted, intent=$prepareResult", isError = true)
                    error("VPN-разрешение не выдано")
                }
                diag("VpnService.prepare() == null — permission already granted")

                diag("ensureVpnService(): instance currently ${if (RawTunVpnService.instance == null) "NULL" else "already set"}")
                ensureVpnService(app)
                val vpn = waitForVpnService()
                if (vpn == null) {
                    diag("waitForVpnService() TIMED OUT after 8s — RawTunVpnService.onCreate() never ran (service not started by Android?)", isError = true)
                    error("RawTunVpnService не поднялся")
                }
                diag("RawTunVpnService instance obtained")

                val pfd = vpn.establish(ip, dnsCsv, mtu)
                running = true
                diag("Builder.establish() returned pfd, fd=${pfd.fd}")
                TunnelManager.addNetworkLog("[RAW] TUN поднят (ip=$ip, mtu=$mtu), жду go_client…")

                // Блокируется до подключения go_client — тот уже печатает
                // "[RAW] Ожидание TUN fd..." и переподключается сам, так что
                // здесь можно просто ждать без доп. таймаута поверх сокета.
                try {
                    diag("TunFdBridge.sendOnce(sockName=$sockName) — waiting for go_client to connect and receive fd...")
                    TunFdBridge.sendOnce(sockName, pfd)
                    diag("TunFdBridge.sendOnce() returned OK — fd handed off to go_client")
                    TunnelManager.addNetworkLog("[RAW] TUN fd передан в go_client")
                } catch (e: Exception) {
                    diag("TunFdBridge.sendOnce() THREW: ${e.javaClass.simpleName}: ${e.message}", isError = true)
                    running = false
                    throw e
                }
            }
        }

    private fun logBatteryOptimizationState(app: Context) {
        runCatching {
            val pm = app.getSystemService(Context.POWER_SERVICE) as? android.os.PowerManager
            val ignoringOpt = pm?.isIgnoringBatteryOptimizations(app.packageName)
            diag("Battery optimization: isIgnoringBatteryOptimizations=$ignoringOpt")
        }.onFailure { diag("Battery optimization check failed: $it") }
        runCatching {
            val am = app.getSystemService(Context.ACTIVITY_SERVICE) as? ActivityManager
            @Suppress("DEPRECATION")
            val importance = am?.runningAppProcesses?.firstOrNull { it.pid == android.os.Process.myPid() }?.importance
            diag("Process importance: $importance (foreground=100)")
        }.onFailure { diag("Process importance check failed: $it") }
    }

    suspend fun stop(context: Context) = mutex.withLock {
        withContext(Dispatchers.IO) { stopLocked(context) }
    }

    fun onVpnRevoked() {
        running = false
        RawTunVpnService.instance?.closeTun()
        TunnelManager.addNetworkLog("[RAW] VPN отозван системой")
    }

    private fun stopLocked(context: Context) {
        running = false
        RawTunVpnService.instance?.closeTun()
        runCatching {
            context.applicationContext.stopService(
                Intent(context.applicationContext, RawTunVpnService::class.java)
            )
        }
        Log.i(TAG, "raw-tun stopped")
    }

    private fun ensureVpnService(app: Context) {
        if (RawTunVpnService.instance != null) return
        val intent = Intent(app, RawTunVpnService::class.java)
        app.startService(intent)
    }

    private suspend fun waitForVpnService(): RawTunVpnService? {
        return withTimeoutOrNull(8_000) {
            while (RawTunVpnService.instance == null) {
                delay(50)
            }
            RawTunVpnService.instance
        }
    }
}
