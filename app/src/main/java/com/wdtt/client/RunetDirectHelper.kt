package com.wdtt.client

import android.content.Context
import android.util.Log
import java.util.zip.GZIPInputStream

/** AllowedIPs for WireGuard: весь IPv4-интернет без российских подсетей (ipdeny ru.zone). */
object RunetDirectHelper {
    private var cachedAllowedIps: String? = null

    fun allowedIpsV4(context: Context): String {
        cachedAllowedIps?.let { return it }
        val loaded = context.applicationContext.resources
            .openRawResource(R.raw.allowed_ips_no_ru_gz)
            .use { input ->
                GZIPInputStream(input).bufferedReader().readText().trim()
            }
        require(loaded.isNotEmpty()) { "Runet direct routes resource is empty" }
        cachedAllowedIps = loaded
        Log.d("RunetDirect", "Loaded ${loaded.count { it == ',' } + 1} IPv4 routes")
        return loaded
    }
}
