package com.wdtt.client

import android.content.Context
import android.util.Log
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.TimeoutCancellationException
import java.io.BufferedReader
import java.io.InputStreamReader

data class DiagnosticStep(val id: String, val state: String, val message: String)
data class DiagnosticReport(val steps: List<DiagnosticStep>, val rttMs: Long?, val error: String?)

object ConnectionDiagnostic {
    suspend fun run(
        context: Context,
        profile: ConnectionProfile,
        onUpdate: (List<DiagnosticStep>) -> Unit
    ): DiagnosticReport = withContext(Dispatchers.IO) {
        val steps = mutableListOf<DiagnosticStep>()
        fun update(id: String, state: String, message: String) {
            steps.removeAll { it.id == id }
            steps += DiagnosticStep(id, state, message)
            onUpdate(steps.toList())
        }

        var process: Process? = null
        var rtt: Long? = null
        var error: String? = null
        try {
            val binaryPath = context.applicationInfo.nativeLibraryDir + "/libclient.so"
            if (!java.io.File(binaryPath).exists()) {
                return@withContext DiagnosticReport(emptyList(), null, "Не найден модуль подключения приложения")
            }
            val hash = profile.vkHashes.split(Regex("[,\\s\\n]+")).firstOrNull { it.isNotBlank() }
            if (hash.isNullOrBlank()) {
                return@withContext DiagnosticReport(emptyList(), null, "В профиле нет хеша VK-звонка")
            }
            if (profile.password.isBlank()) {
                return@withContext DiagnosticReport(emptyList(), null, "В профиле нет пароля подключения")
            }
            val store = SettingsStore(context)
            val port = if (store.manualPortsEnabled.first()) store.serverDtlsPort.first() else 56000
            val peer = PeerAddress.ensurePort(profile.peer, port)
            val command = listOf(
                binaryPath, "-diagnose", "-peer", peer, "-vk", hash,
                "-password", profile.password, "-go-dns", store.resolveGoDnsArg()
            )
            val started = ProcessBuilder(command)
                .directory(context.filesDir)
                .redirectErrorStream(true)
                .apply { environment()["LD_LIBRARY_PATH"] = context.applicationInfo.nativeLibraryDir }
                .start()
            process = started
            val reader = BufferedReader(InputStreamReader(started.inputStream))
            val readerThread = Thread {
                try {
                    reader.forEachLine { line ->
                        when {
                            line.startsWith("DIAG|") -> {
                                val parts = line.split("|", limit = 4)
                                if (parts.size == 4) update(parts[1], parts[2], parts[3])
                            }
                            line.startsWith("DIAG_RESULT|") -> rtt = line.substringAfter("|").toLongOrNull()
                            line.startsWith("DIAG_ERROR|") -> error = line.substringAfter("|", "Ошибка диагностики")
                        }
                    }
                } catch (e: Exception) {
                    Log.e("ConnectionDiagnostic", "Не удалось прочитать диагностику", e)
                }
            }
            readerThread.start()
            try {
                withTimeout(35000L) {
                    while (started.isAlive) delay(100)
                }
            } catch (_: TimeoutCancellationException) {
                error = "Превышено время ожидания (35 с)"
                started.destroyForcibly()
            }
            readerThread.join(500)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Log.e("ConnectionDiagnostic", "Ошибка диагностики", e)
            error = e.message ?: "Не удалось запустить диагностику"
        } finally {
            process?.takeIf { it.isAlive }?.destroyForcibly()
        }
        DiagnosticReport(steps.toList(), rtt, error)
    }
}
