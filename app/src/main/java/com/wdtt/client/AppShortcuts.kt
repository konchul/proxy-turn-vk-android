package com.wdtt.client

import android.content.Context
import android.content.pm.ShortcutManager
import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

object AppShortcuts {
    const val ACTION_START_TUNNEL = "com.wdtt.client.shortcut.START_TUNNEL"
    const val ACTION_STOP_TUNNEL = "com.wdtt.client.shortcut.STOP_TUNNEL"

    private val removedShortcutIds = listOf(
        "shortcut_add_profile",
        "shortcut_toggle_tunnel",
        "shortcut_toggle_vk_mode",
    )

    fun refreshAsync(context: Context) {
        if (Build.VERSION.SDK_INT < 25) return
        CoroutineScope(Dispatchers.IO).launch {
            cleanupRemovedShortcuts(context.applicationContext)
        }
    }

    private fun cleanupRemovedShortcuts(context: Context) {
        if (Build.VERSION.SDK_INT < 25) return
        val shortcutManager = context.getSystemService(ShortcutManager::class.java) ?: return
        runCatching {
            shortcutManager.disableShortcuts(removedShortcutIds)
            shortcutManager.removeDynamicShortcuts(removedShortcutIds)
        }
    }
}
