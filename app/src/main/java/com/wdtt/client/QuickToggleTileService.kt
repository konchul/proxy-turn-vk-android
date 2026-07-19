package com.wdtt.client

import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

class QuickToggleTileService : TileService() {

    override fun onStartListening() {
        super.onStartListening()
        updateTileState()
    }

    override fun onClick() {
        super.onClick()
        val willRun = !TunnelManager.running.value
        TunnelControl.toggle(applicationContext)
        updateTile(willRun)
    }

    private fun updateTileState() {
        updateTile(TunnelManager.running.value)
    }

    private fun updateTile(running: Boolean) {
        val tile = qsTile ?: return
        if (running) {
            tile.state = Tile.STATE_ACTIVE
            tile.label = "qWDTT: Вкл"
            if (Build.VERSION.SDK_INT >= 29) {
                tile.subtitle = "Активен"
            }
        } else {
            tile.state = Tile.STATE_INACTIVE
            tile.label = "qWDTT: Выкл"
            if (Build.VERSION.SDK_INT >= 29) {
                tile.subtitle = "Отключен"
            }
        }
        tile.updateTile()
    }

    companion object {
        fun requestTileUpdate(context: Context) {
            if (Build.VERSION.SDK_INT >= 24) {
                try {
                    requestListeningState(
                        context,
                        ComponentName(context, QuickToggleTileService::class.java)
                    )
                } catch (e: Exception) {
                    e.printStackTrace()
                }
            }
        }
    }
}
