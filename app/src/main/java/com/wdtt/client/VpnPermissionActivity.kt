package com.wdtt.client

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.result.contract.ActivityResultContracts

/**
 * Прозрачный экран только для системного VPN-consent.
 * Используется виджетом и плиткой Quick Settings — без открытия главного UI.
 */
class VpnPermissionActivity : ComponentActivity() {
    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) {
        openMainActivityAndFinish()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState != null) return

        val permissionIntent = runCatching { VpnService.prepare(this) }.getOrNull()
        if (permissionIntent == null) {
            openMainActivityAndFinish()
        } else {
            vpnPermissionLauncher.launch(permissionIntent)
        }
    }

    private fun openMainActivityAndFinish() {
        startActivity(
            Intent(this, MainActivity::class.java).apply {
                addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
            }
        )
        finish()
    }
}
