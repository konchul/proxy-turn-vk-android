package com.wdtt.client

import android.content.Context
import android.content.Intent
import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

object  TunnelControl {

    fun toggle(context: Context) {
        if (TunnelManager.running.value) {
            stop(context)
        } else {
            startFromSavedSettings(context)
        }
    }

    fun stop(context: Context) {
        val stopIntent = Intent(context, TunnelService::class.java).apply { action = "STOP" }
        context.startService(stopIntent)
    }

    fun startFromSavedSettings(context: Context) {
        val appContext = context.applicationContext
        CoroutineScope(Dispatchers.IO).launch {
            SettingsStore.awaitMigrations(appContext)
            val store = SettingsStore(appContext)
            val basePeer = store.peer.first()
            val hashes = store.vkHashes.first()
            val workers = store.workersPerHash.first()
            val port = store.listenPort.first()
            val password = store.connectionPassword.first()
            val captchaMode = store.captchaMode.first()
            val captchaMethod = store.captchaSolveMethod.first()
            val vkAnonPath = SettingsStore.normalizeVkAnonPath(store.vkAnonPath.first())
            val goDnsArg = store.resolveGoDnsArg()
            val obfsMode = SettingsStore.normalizeObfsMode(store.obfsMode.first())
            val manualPortsEnabled = store.manualPortsEnabled.first()
            val serverDtlsPort = if (manualPortsEnabled) store.serverDtlsPort.first() else 56000
            val peerWithPort = if (basePeer.isBlank()) basePeer else PeerAddress.ensurePort(basePeer, serverDtlsPort)

            if (peerWithPort.isBlank() || hashes.isBlank() || password.isBlank()) {
                return@launch
            }

            val startIntent = Intent(appContext, TunnelService::class.java).apply {
                action = "START_FORCED"
                putExtra("peer", peerWithPort)
                putExtra("vk_hashes", hashes)
                putExtra("secondary_vk_hash", "")
                putExtra("workers_per_hash", workers)
                putExtra("port", port)
                putExtra("sni", store.sni.first())
                putExtra("connection_password", password)
                putExtra("captcha_mode", captchaMode)
                putExtra("captcha_solve_method", captchaMethod)
                putExtra("vk_anon_path", vkAnonPath)
                putExtra("go_dns_arg", goDnsArg)
                putExtra("obfs_mode", obfsMode)
            }

            withContext(Dispatchers.Main) {
                if (Build.VERSION.SDK_INT >= 26) {
                    appContext.startForegroundService(startIntent)
                } else {
                    appContext.startService(startIntent)
                }
            }
        }
    }
}
