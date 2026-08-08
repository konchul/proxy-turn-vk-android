package com.wdtt.client

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.FormBody
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONArray
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * Клиент к admin HTTP API сервера (см. server.go/admin_api.go, /admin/passwords...) —
 * то же управление паролями/устройствами, что уже умеет Telegram-бот, просто
 * второй способ дёрнуть ту же логику. Авторизация — deploy-пароль владельца
 * (X-Admin-Password), тот же что и для SSH-деплоя.
 */
object AdminApiClient {

    data class AdminPassword(
        val password: String,
        val label: String,
        val vkHash: String,
        val ports: String,
        val maxDevices: Int,
        val deviceIds: List<String>,
        val expiresAt: Long,
        val isDeactivated: Boolean,
        val downBytes: Long,
        val upBytes: Long,
        val activeDevices: Int,
    )

    sealed class Result<out T> {
        data class Success<T>(val value: T) : Result<T>()
        data class Failure(val message: String) : Result<Nothing>()
    }

    private val client = OkHttpClient.Builder()
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(8, TimeUnit.SECONDS)
        .writeTimeout(8, TimeUnit.SECONDS)
        .build()

    private fun baseUrl(host: String, port: Int): String = "http://$host:$port"

    private fun Exception.readableMessage(): String {
        val text = message
        return if (text.isNullOrBlank()) this::class.java.simpleName else "${this::class.java.simpleName}: $text"
    }

    private fun parsePassword(o: JSONObject): AdminPassword {
        val deviceIds = mutableListOf<String>()
        o.optJSONArray("device_ids")?.let { arr ->
            for (i in 0 until arr.length()) deviceIds.add(arr.optString(i))
        }
        return AdminPassword(
            password = o.optString("password"),
            label = o.optString("label"),
            vkHash = o.optString("vk_hash"),
            ports = o.optString("ports"),
            maxDevices = o.optInt("max_devices", 1),
            deviceIds = deviceIds,
            expiresAt = o.optLong("expires_at", 0L),
            isDeactivated = o.optBoolean("is_deactivated", false),
            downBytes = o.optLong("down_bytes", 0L),
            upBytes = o.optLong("up_bytes", 0L),
            activeDevices = o.optInt("active_devices", 0),
        )
    }

    private fun errorMessage(bodyText: String?, fallback: String): String {
        if (bodyText.isNullOrBlank()) return fallback
        return runCatching { JSONObject(bodyText).optString("error") }
            .getOrNull()
            ?.takeIf { it.isNotBlank() }
            ?: fallback
    }

    suspend fun listPasswords(host: String, port: Int, adminPassword: String): Result<List<AdminPassword>> =
        withContext(Dispatchers.IO) {
            try {
                val request = Request.Builder()
                    .url("${baseUrl(host, port)}/admin/passwords")
                    .header("X-Admin-Password", adminPassword)
                    .get()
                    .build()
                client.newCall(request).execute().use { resp ->
                    val text = resp.body?.string()
                    if (!resp.isSuccessful) {
                        return@withContext Result.Failure(errorMessage(text, "HTTP ${resp.code}"))
                    }
                    val arr: JSONArray = JSONObject(text ?: "{}").optJSONArray("passwords") ?: JSONArray()
                    val list = (0 until arr.length()).map { parsePassword(arr.getJSONObject(it)) }
                    Result.Success(list)
                }
            } catch (e: Exception) {
                Result.Failure(e.readableMessage())
            }
        }

    suspend fun createPassword(
        host: String,
        port: Int,
        adminPassword: String,
        vkHash: String,
        days: Int,
        maxDevices: Int,
        label: String = "",
    ): Result<AdminPassword> = withContext(Dispatchers.IO) {
        try {
            val formBuilder = FormBody.Builder()
                .add("vk_hash", vkHash)
                .add("days", days.toString())
                .add("max_devices", maxDevices.toString())
            if (label.isNotBlank()) formBuilder.add("label", label)
            val request = Request.Builder()
                .url("${baseUrl(host, port)}/admin/passwords")
                .header("X-Admin-Password", adminPassword)
                .post(formBuilder.build())
                .build()
            client.newCall(request).execute().use { resp ->
                val text = resp.body?.string()
                if (!resp.isSuccessful) {
                    return@withContext Result.Failure(errorMessage(text, "HTTP ${resp.code}"))
                }
                Result.Success(parsePassword(JSONObject(text ?: "{}")))
            }
        } catch (e: Exception) {
            Result.Failure(e.readableMessage())
        }
    }

    /**
     * Редактирование уже существующего пароля — обновляет только присланные
     * поля (label/vkHash/maxDevices/days), остальное на сервере не меняется.
     * null означает "не трогать это поле".
     */
    suspend fun updatePassword(
        host: String,
        port: Int,
        adminPassword: String,
        password: String,
        label: String? = null,
        vkHash: String? = null,
        maxDevices: Int? = null,
        days: Int? = null,
    ): Result<AdminPassword> = withContext(Dispatchers.IO) {
        try {
            val formBuilder = FormBody.Builder().add("password", password)
            label?.let { formBuilder.add("label", it) }
            vkHash?.let { formBuilder.add("vk_hash", it) }
            maxDevices?.let { formBuilder.add("max_devices", it.toString()) }
            days?.let { formBuilder.add("days", it.toString()) }
            val request = Request.Builder()
                .url("${baseUrl(host, port)}/admin/passwords/update")
                .header("X-Admin-Password", adminPassword)
                .post(formBuilder.build())
                .build()
            client.newCall(request).execute().use { resp ->
                val text = resp.body?.string()
                if (!resp.isSuccessful) {
                    return@withContext Result.Failure(errorMessage(text, "HTTP ${resp.code}"))
                }
                Result.Success(parsePassword(JSONObject(text ?: "{}")))
            }
        } catch (e: Exception) {
            Result.Failure(e.readableMessage())
        }
    }

    private suspend fun postPasswordAction(
        host: String,
        port: Int,
        adminPassword: String,
        path: String,
        password: String,
    ): Result<AdminPassword> = withContext(Dispatchers.IO) {
        try {
            val formBody = FormBody.Builder().add("password", password).build()
            val request = Request.Builder()
                .url("${baseUrl(host, port)}$path")
                .header("X-Admin-Password", adminPassword)
                .post(formBody)
                .build()
            client.newCall(request).execute().use { resp ->
                val text = resp.body?.string()
                if (!resp.isSuccessful) {
                    return@withContext Result.Failure(errorMessage(text, "HTTP ${resp.code}"))
                }
                Result.Success(parsePassword(JSONObject(text ?: "{}")))
            }
        } catch (e: Exception) {
            Result.Failure(e.readableMessage())
        }
    }

    suspend fun deactivatePassword(host: String, port: Int, adminPassword: String, password: String) =
        postPasswordAction(host, port, adminPassword, "/admin/passwords/deactivate", password)

    suspend fun activatePassword(host: String, port: Int, adminPassword: String, password: String) =
        postPasswordAction(host, port, adminPassword, "/admin/passwords/activate", password)

    /**
     * Открепить одно устройство от пароля (снимает WG-пир и освобождает слот
     * устройства, сам пароль остаётся активным). См. unbindDevices на сервере.
     */
    suspend fun unbindDevice(
        host: String,
        port: Int,
        adminPassword: String,
        password: String,
        deviceId: String,
    ): Result<AdminPassword> = withContext(Dispatchers.IO) {
        try {
            val formBody = FormBody.Builder()
                .add("password", password)
                .add("device_id", deviceId)
                .build()
            val request = Request.Builder()
                .url("${baseUrl(host, port)}/admin/passwords/unbind-device")
                .header("X-Admin-Password", adminPassword)
                .post(formBody)
                .build()
            client.newCall(request).execute().use { resp ->
                val text = resp.body?.string()
                if (!resp.isSuccessful) {
                    return@withContext Result.Failure(errorMessage(text, "HTTP ${resp.code}"))
                }
                Result.Success(parsePassword(JSONObject(text ?: "{}")))
            }
        } catch (e: Exception) {
            Result.Failure(e.readableMessage())
        }
    }

    suspend fun deletePassword(host: String, port: Int, adminPassword: String, password: String): Result<Unit> =
        withContext(Dispatchers.IO) {
            try {
                val formBody = FormBody.Builder().add("password", password).build()
                val request = Request.Builder()
                    .url("${baseUrl(host, port)}/admin/passwords/delete")
                    .header("X-Admin-Password", adminPassword)
                    .post(formBody)
                    .build()
                client.newCall(request).execute().use { resp ->
                    val text = resp.body?.string()
                    if (!resp.isSuccessful) {
                        return@withContext Result.Failure(errorMessage(text, "HTTP ${resp.code}"))
                    }
                    Result.Success(Unit)
                }
            } catch (e: Exception) {
                Result.Failure(e.readableMessage())
            }
        }
}
