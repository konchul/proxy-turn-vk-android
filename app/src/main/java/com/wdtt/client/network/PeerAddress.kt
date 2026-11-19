package com.wdtt.client

object PeerAddress {
    fun hasExplicitPort(peer: String): Boolean {
        val trimmed = peer.trim()
        if (trimmed.isEmpty()) return false
        if (trimmed.startsWith("[")) {
            val close = trimmed.indexOf(']')
            return close != -1 && trimmed.length > close + 1 && trimmed[close + 1] == ':'
        }
        val lastColon = trimmed.lastIndexOf(':')
        if (lastColon <= 0 || trimmed.indexOf(':') != lastColon) return false
        val portPart = trimmed.substring(lastColon + 1)
        return portPart.isNotEmpty() && portPart.all(Char::isDigit)
    }

    fun host(peer: String): String {
        val trimmed = peer.trim()
        if (trimmed.startsWith("[")) {
            val close = trimmed.indexOf(']')
            if (close > 0) return trimmed.substring(1, close)
        }
        if (!hasExplicitPort(trimmed)) return trimmed
        return trimmed.substring(0, trimmed.lastIndexOf(':'))
    }

    fun port(peer: String): Int? {
        val trimmed = peer.trim()
        if (!hasExplicitPort(trimmed)) return null
        return trimmed.substringAfterLast(':').toIntOrNull()
    }

    fun ensurePort(peer: String, defaultPort: Int): String {
        val trimmed = peer.trim()
        if (trimmed.isEmpty()) return trimmed
        if (hasExplicitPort(trimmed)) return trimmed
        return withPort(trimmed, defaultPort)
    }

    /** В отличие от [ensurePort] — всегда заменяет порт на [newPort], даже если он уже был указан. */
    fun withPort(peer: String, newPort: Int): String {
        val trimmed = peer.trim()
        if (trimmed.isEmpty()) return trimmed
        val address = host(trimmed)
        return if (address.contains(':')) "[$address]:$newPort" else "$address:$newPort"
    }

    fun httpEndpoint(peer: String, defaultPort: Int): String {
        val trimmed = peer.trim()
        val effectivePort = port(trimmed) ?: defaultPort
        val effectiveHost = host(trimmed)
        val hostForUrl = if (effectiveHost.contains(':')) "[$effectiveHost]" else effectiveHost
        return "$hostForUrl:$effectivePort"
    }
}
