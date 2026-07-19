package com.wdtt.client

import android.content.Context
import android.content.pm.ShortcutManager
import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

object AppShortcuts {
    const val ACTION_ADD_PROFILE = "com.wdtt.client.shortcut.ADD_PROFILE"

    const val ID_ADD_PROFILE = "shortcut_add_profile"

    private val removedShortcutIds = listOf(
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
