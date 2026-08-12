package com.wdtt.client.ui

import android.content.Intent
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.compose.animation.Crossfade
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.DismissDirection
import androidx.compose.material.DismissValue
import androidx.compose.material.ExperimentalMaterialApi
import androidx.compose.material.SwipeToDismiss
import androidx.compose.material.rememberDismissState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.CloudUpload
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.wdtt.client.ManagedServer
import com.wdtt.client.ServersStore
import kotlinx.coroutines.launch

/**
 * Единая вкладка "Серверы" — список серверов на главном экране, тап по
 * карточке открывает доступы (бывший AdminTab), отдельная иконка на карточке
 * открывает деплой (бывший DeployTab), кнопка "+" создаёт новый сервер.
 * Мульти-деплой на несколько серверов сразу доступен прямо со списка через
 * режим множественного выбора (переиспользует performMultiDeploy из
 * DeployTab.kt).
 */
private sealed class ServersTabScreen {
    object ServerList : ServersTabScreen()
    data class AccessList(val serverId: String) : ServersTabScreen()
    data class ServerDeploy(val serverId: String?) : ServersTabScreen()
}

@Composable
fun ServersTab() {
    val context = LocalContext.current
    val serversStore = remember { ServersStore(context) }

    var screen by rememberSaveable(stateSaver = ServersTabScreenSaver) {
        mutableStateOf<ServersTabScreen>(ServersTabScreen.ServerList)
    }

    // На экранах конкретного сервера системный жест «Назад» должен вернуться
    // к списку серверов, а не закрыть Activity.
    BackHandler(enabled = screen !is ServersTabScreen.ServerList) {
        screen = ServersTabScreen.ServerList
    }

    Crossfade(targetState = screen, label = "servers_tab_content") { current ->
        when (val s = current) {
            is ServersTabScreen.ServerList -> ServerListScreen(
                serversStore = serversStore,
                onOpenAccess = { id -> screen = ServersTabScreen.AccessList(id) },
                onOpenDeploy = { id -> screen = ServersTabScreen.ServerDeploy(id) },
                onAddServer = { screen = ServersTabScreen.ServerDeploy(null) },
            )
            is ServersTabScreen.AccessList -> AccessListHost(
                serversStore = serversStore,
                serverId = s.serverId,
                onBack = { screen = ServersTabScreen.ServerList },
            )
            is ServersTabScreen.ServerDeploy -> DeployScreen(
                initialServerId = s.serverId,
                onBack = { screen = ServersTabScreen.ServerList },
            )
        }
    }
}

/**
 * Простой saver для sealed-состояния навигации: сериализуем в пару
 * (тип, id-или-null) строками — тот же подход, что и остальной rememberSaveable
 * в этом файле, без внешних зависимостей вроде kotlinx.serialization.
 */
private val ServersTabScreenSaver = androidx.compose.runtime.saveable.Saver<ServersTabScreen, List<String>>(
    save = { state ->
        when (state) {
            is ServersTabScreen.ServerList -> listOf("list")
            is ServersTabScreen.AccessList -> listOf("access", state.serverId)
            is ServersTabScreen.ServerDeploy -> listOf("deploy", state.serverId ?: "")
        }
    },
    restore = { saved ->
        when (saved.getOrNull(0)) {
            "access" -> ServersTabScreen.AccessList(saved.getOrElse(1) { "" })
            "deploy" -> ServersTabScreen.ServerDeploy(saved.getOrNull(1)?.ifEmpty { null })
            else -> ServersTabScreen.ServerList
        }
    }
)

/**
 * Ждём появления сервера с нужным id в потоке ServersStore.servers (после
 * навигации из ServerList он гарантированно там уже есть) и передаём его в
 * AccessListScreen. Если сервер вдруг исчез (удалили в другом месте) —
 * откатываемся на список.
 */
@Composable
private fun AccessListHost(
    serversStore: ServersStore,
    serverId: String,
    onBack: () -> Unit,
) {
    val servers by serversStore.servers.collectAsStateWithLifecycle(initialValue = emptyList())
    val server = servers.find { it.id == serverId }

    LaunchedEffect(servers, serverId) {
        if (servers.isNotEmpty() && server == null) {
            onBack()
        }
    }

    if (server != null) {
        AccessListScreen(server = server, onBack = onBack)
    } else {
        Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ServerListScreen(
    serversStore: ServersStore,
    onOpenAccess: (String) -> Unit,
    onOpenDeploy: (String) -> Unit,
    onAddServer: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    LaunchedEffect(Unit) { serversStore.migrateLegacyServerIfNeeded() }
    val servers by serversStore.servers.collectAsStateWithLifecycle(initialValue = emptyList())

    var multiSelectMode by rememberSaveable { mutableStateOf(false) }
    var selectedForDeploy by rememberSaveable { mutableStateOf<Set<String>>(emptySet()) }
    var showMultiDeployConfirm by remember { mutableStateOf(false) }
    var multiDeployResults by remember { mutableStateOf<Map<String, DeployOutcome>>(emptyMap()) }
    var isMultiDeploying by remember { mutableStateOf(false) }
    var activeDeployingServerId by remember { mutableStateOf<String?>(null) }
    var multiDeployJob by remember { mutableStateOf<kotlinx.coroutines.Job?>(null) }
    var deleteTarget by remember { mutableStateOf<ManagedServer?>(null) }
    var renameTarget by remember { mutableStateOf<ManagedServer?>(null) }

    fun exitMultiSelect() {
        multiSelectMode = false
        selectedForDeploy = emptySet()
        multiDeployResults = emptyMap()
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 20.dp, vertical = 16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "Серверы",
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface,
                modifier = Modifier.weight(1f)
            )
            if (servers.size > 1) {
                IconButton(onClick = {
                    if (multiSelectMode) exitMultiSelect() else multiSelectMode = true
                }) {
                    Icon(
                        Icons.Filled.CloudUpload,
                        contentDescription = if (multiSelectMode) "Отменить выбор" else "Деплой на несколько серверов",
                        tint = if (multiSelectMode) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            IconButton(onClick = onAddServer) {
                Icon(
                    Icons.Filled.Add,
                    contentDescription = "Добавить сервер",
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }

        if (servers.isEmpty()) {
            Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(24.dp),
                verticalArrangement = Arrangement.Center,
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Icon(
                    Icons.Filled.Dns,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.height(48.dp)
                )
                Spacer(modifier = Modifier.height(12.dp))
                Text(
                    "Серверов пока нет — нажмите «+», чтобы добавить и развернуть первый",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        } else {
            LazyColumn(
                modifier = Modifier.weight(1f),
                contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 4.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                items(servers, key = { it.id }) { server ->
                    if (multiSelectMode) {
                        ServerCard(
                            server = server,
                            multiSelectMode = true,
                            isChecked = selectedForDeploy.contains(server.id),
                            onCheckedChange = { checked ->
                                selectedForDeploy = if (checked) selectedForDeploy + server.id else selectedForDeploy - server.id
                            },
                            onOpenAccess = { onOpenAccess(server.id) },
                            onOpenDeploy = { onOpenDeploy(server.id) },
                            onRenameRequest = {},
                        )
                    } else {
                        SwipeToDeleteServerCard(
                            server = server,
                            onOpenAccess = { onOpenAccess(server.id) },
                            onOpenDeploy = { onOpenDeploy(server.id) },
                            onRequestDelete = { deleteTarget = server },
                            onRenameRequest = { renameTarget = server },
                        )
                    }
                }
                item { Spacer(modifier = Modifier.height(if (selectedForDeploy.isNotEmpty()) 96.dp else 24.dp)) }
            }
        }

        if (multiSelectMode && selectedForDeploy.isNotEmpty()) {
            Surface(
                modifier = Modifier.fillMaxWidth(),
                color = MaterialTheme.colorScheme.background,
            ) {
                Button(
                    onClick = { showMultiDeployConfirm = true },
                    modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 12.dp).height(48.dp),
                    shape = RoundedCornerShape(16.dp),
                    colors = ButtonDefaults.buttonColors(contentColor = MaterialTheme.colorScheme.onPrimary),
                ) {
                    Text("Установить на выбранные (${selectedForDeploy.size})", fontWeight = FontWeight.SemiBold)
                }
            }
        }
    }

    if (showMultiDeployConfirm) {
        val targets = servers.filter { selectedForDeploy.contains(it.id) }
        MultiDeployConfirmDialog(
            servers = targets,
            onDismiss = { showMultiDeployConfirm = false },
            onConfirm = {
                showMultiDeployConfirm = false
                val appContext = context.applicationContext
                multiDeployResults = targets.associate { it.id to DeployOutcome.InProgress }
                multiDeployJob = scope.launch {
                    isMultiDeploying = true
                    // DEPLOY_START/DEPLOY_STOP держат foreground-сервис и wake
                    // lock живыми на время деплоя — рассчитаны на ОДИН SSH-сеанс.
                    // Раньше они дёргались на каждый сервер внутри цикла, из-за
                    // чего DEPLOY_STOP после первого сервера гасил foreground-
                    // защиту (stopTunnel(), если туннель не запущен) ещё до
                    // того, как второй сервер начинал разворачиваться — систему
                    // успевало подморозить процесс между серверами, и второй
                    // деплой либо не стартовал, либо тихо обрывался. Теперь
                    // держим сервис живым один раз на ВЕСЬ мультидеплой.
                    val startIntent = Intent(appContext, com.wdtt.client.TunnelService::class.java).apply { action = "DEPLOY_START" }
                    if (Build.VERSION.SDK_INT >= 26) appContext.startForegroundService(startIntent) else appContext.startService(startIntent)
                    try {
                        performMultiDeploy(
                            context = appContext,
                            servers = targets,
                            onServerStarted = { id ->
                                activeDeployingServerId = id
                                multiDeployResults = multiDeployResults + (id to DeployOutcome.InProgress)
                            },
                            onServerFinished = { id, outcome ->
                                multiDeployResults = multiDeployResults + (id to outcome)
                            },
                        )
                    } finally {
                        isMultiDeploying = false
                        activeDeployingServerId = null
                        multiDeployJob = null
                        try {
                            appContext.startService(Intent(appContext, com.wdtt.client.TunnelService::class.java).apply { action = "DEPLOY_STOP" })
                        } catch (_: Exception) {}
                        exitMultiSelect()
                    }
                }
            }
        )
    }

    if (isMultiDeploying) {
        val targets = servers.filter { selectedForDeploy.contains(it.id) }
        MultiDeployBlockingDialog(
            servers = targets,
            results = multiDeployResults,
            activeServerId = activeDeployingServerId,
            onCancel = { multiDeployJob?.cancel() },
        )
    }

    deleteTarget?.let { target ->
        AlertDialog(
            onDismissRequest = { deleteTarget = null },
            title = {
                Text(
                    "Удалить сервер?",
                    fontWeight = FontWeight.Bold,
                    style = MaterialTheme.typography.titleLarge
                )
            },
            text = {
                Text("Вы действительно хотите удалить сервер «${target.name.ifBlank { target.ip }}»?\n\nЭто удалит только сохранённые данные подключения в приложении — сам сервер и всё, что на нём развёрнуто, не пострадает.")
            },
            confirmButton = {
                Button(
                    onClick = {
                        scope.launch { serversStore.deleteServer(target.id) }
                        deleteTarget = null
                    },
                    colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error),
                    shape = RoundedCornerShape(12.dp)
                ) {
                    Text("Удалить", color = MaterialTheme.colorScheme.onError)
                }
            },
            dismissButton = {
                TextButton(onClick = { deleteTarget = null }) {
                    Text("Отмена")
                }
            }
        )
    }

    renameTarget?.let { target ->
        SaveServerNameDialog(
            initialName = target.name.ifBlank { target.ip },
            onDismiss = { renameTarget = null },
            onConfirm = { newName ->
                scope.launch { serversStore.updateServer(target.copy(name = newName)) }
                renameTarget = null
            }
        )
    }
}

@OptIn(ExperimentalMaterialApi::class)
@Composable
private fun SwipeToDeleteServerCard(
    server: ManagedServer,
    onOpenAccess: () -> Unit,
    onOpenDeploy: () -> Unit,
    onRequestDelete: () -> Unit,
    onRenameRequest: () -> Unit,
) {
    val dismissState = rememberDismissState(
        confirmStateChange = { value ->
            if (value == DismissValue.DismissedToStart) {
                onRequestDelete()
            }
            false
        }
    )

    SwipeToDismiss(
        state = dismissState,
        directions = setOf(DismissDirection.EndToStart),
        background = {
            if (dismissState.dismissDirection != null) {
                Box(
                    modifier = Modifier
                        .fillMaxSize()
                        .clip(RoundedCornerShape(28.dp))
                        .background(MaterialTheme.colorScheme.errorContainer),
                    contentAlignment = Alignment.CenterEnd
                ) {
                    Icon(
                        Icons.Filled.Delete,
                        contentDescription = "Удалить",
                        tint = MaterialTheme.colorScheme.onErrorContainer,
                        modifier = Modifier.padding(end = 32.dp).size(28.dp)
                    )
                }
            }
        }
    ) {
        ServerCard(
            server = server,
            multiSelectMode = false,
            isChecked = false,
            onCheckedChange = {},
            onOpenAccess = onOpenAccess,
            onOpenDeploy = onOpenDeploy,
            onRenameRequest = onRenameRequest,
        )
    }
}

@Composable
private fun ServerCard(
    server: ManagedServer,
    multiSelectMode: Boolean,
    isChecked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    onOpenAccess: () -> Unit,
    onOpenDeploy: () -> Unit,
    onRenameRequest: () -> Unit,
) {
    AppSectionCard(
        modifier = Modifier.clickable(enabled = !multiSelectMode, onClick = onOpenAccess),
        contentPadding = PaddingValues(horizontal = 18.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
        shadowElevation = 0.dp,
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (multiSelectMode) {
                Checkbox(checked = isChecked, onCheckedChange = onCheckedChange)
                Spacer(modifier = Modifier.width(4.dp))
            }
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    server.name.ifBlank { server.ip },
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                if (server.name.isNotBlank() && server.name != server.ip) {
                    Spacer(modifier = Modifier.height(2.dp))
                    Text(
                        server.ip,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Spacer(modifier = Modifier.height(2.dp))
                Text(
                    if (server.manualPortsEnabled) "SSH ${server.sshPort} · WG ${server.wgPort}" else "SSH ${server.sshPort}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.7f),
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (!multiSelectMode) {
                IconButton(onClick = onRenameRequest) {
                    Icon(
                        Icons.Filled.Edit,
                        contentDescription = "Переименовать",
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                IconButton(onClick = onOpenDeploy) {
                    Icon(
                        Icons.Filled.CloudUpload,
                        contentDescription = "Деплой",
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun MultiDeployBlockingDialog(
    servers: List<ManagedServer>,
    results: Map<String, DeployOutcome>,
    activeServerId: String?,
    onCancel: () -> Unit,
) {
    androidx.compose.ui.window.Dialog(
        onDismissRequest = {},
        properties = androidx.compose.ui.window.DialogProperties(
            dismissOnBackPress = false,
            dismissOnClickOutside = false,
        ),
    ) {
        Surface(
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(28.dp),
            color = MaterialTheme.colorScheme.surface,
            tonalElevation = 8.dp,
        ) {
            Column(
                modifier = Modifier.padding(24.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                Text(
                    "Установка на серверы",
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.onSurface,
                )
                Text(
                    "Не закрывайте приложение, пока идёт установка",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(modifier = Modifier.height(16.dp))
                Column(verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    servers.forEach { server ->
                        val outcome = results[server.id] ?: DeployOutcome.InProgress
                        MultiDeployStatusRow(
                            name = server.name.ifBlank { server.ip },
                            outcome = outcome,
                            showLiveProgress = server.id == activeServerId,
                        )
                    }
                }
                Spacer(modifier = Modifier.height(20.dp))
                OutlinedButton(
                    onClick = onCancel,
                    modifier = Modifier.fillMaxWidth().height(48.dp),
                    shape = RoundedCornerShape(16.dp),
                ) {
                    Text("Отменить", fontWeight = FontWeight.SemiBold)
                }
            }
        }
    }
}
