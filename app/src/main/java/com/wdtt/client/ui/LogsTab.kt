package com.wdtt.client.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.widget.Toast
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.animation.core.*
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Notes
import androidx.compose.material.icons.filled.Timer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.wdtt.client.ConnectionPipelineCard
import com.wdtt.client.LogEntry
import com.wdtt.client.TunnelManager
import com.wdtt.client.WDTTColors
import kotlinx.coroutines.delay

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LogsTab() {
    val context = LocalContext.current
    val currentLogs by TunnelManager.logs.collectAsStateWithLifecycle()
    val statsText by TunnelManager.stats.collectAsStateWithLifecycle()
    val isRunning by TunnelManager.running.collectAsStateWithLifecycle()
    val isConnecting by TunnelManager.isConnecting.collectAsStateWithLifecycle()
    val connectedSinceMs by TunnelManager.connectedSinceMs.collectAsStateWithLifecycle()
    val pipelineState by TunnelManager.connectionPipeline.collectAsStateWithLifecycle()
    val listState = rememberLazyListState()

    var nowMs by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(isRunning, connectedSinceMs) {
        if (!isRunning || connectedSinceMs <= 0L) return@LaunchedEffect
        while (true) {
            nowMs = System.currentTimeMillis()
            delay(1000)
        }
    }
    val uptimeText = if (isRunning && connectedSinceMs > 0L) {
        TunnelManager.formatUptime(nowMs - connectedSinceMs)
    } else {
        null
    }

    // Синяя статистика закреплена сверху — не тонет среди DNS/VK/капчи.
    val scrollableLogs = remember(currentLogs) {
        currentLogs.filter { it.key != "stats" }
    }
    val pinnedStatsMessage = remember(statsText, isRunning, isConnecting, currentLogs) {
        val fromLog = currentLogs.firstOrNull { it.key == "stats" }?.message
        when {
            !fromLog.isNullOrBlank() -> fromLog
            (isRunning || isConnecting) && statsText.isNotBlank() -> "[СТАТИСТИКА] $statsText"
            else -> null
        }
    }

    Column(modifier = Modifier.fillMaxSize().padding(16.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                "Лог событий",
                style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold),
                color = MaterialTheme.colorScheme.onSurface
            )
            Row {
                IconButton(onClick = { TunnelManager.clearLogs() }) {
                    Icon(Icons.Default.Delete, contentDescription = "Clear", tint = MaterialTheme.colorScheme.primary)
                }
                IconButton(onClick = {
                    val text = buildString {
                        if (pinnedStatsMessage != null) {
                            appendLine(pinnedStatsMessage)
                        }
                        scrollableLogs.forEach { appendLine("${it.message} (x${it.count})") }
                    }.trim()
                    val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    clipboard.setPrimaryClip(ClipData.newPlainText("WDTT Logs", text))
                    Toast.makeText(context, "Скопировано", Toast.LENGTH_SHORT).show()
                }) {
                    Icon(Icons.Default.ContentCopy, contentDescription = "Copy", tint = MaterialTheme.colorScheme.primary)
                }
            }
        }

        val isDark = isSystemInDarkTheme()
        val terminalBg = if (isDark) WDTTColors.terminalBgDark else WDTTColors.terminalBg

        Card(
            modifier = Modifier.fillMaxSize(),
            colors = CardDefaults.cardColors(containerColor = terminalBg),
            shape = RoundedCornerShape(20.dp),
            elevation = CardDefaults.cardElevation(defaultElevation = 6.dp)
        ) {
            Column(modifier = Modifier.fillMaxSize()) {
                if (pinnedStatsMessage != null || uptimeText != null) {
                    Surface(
                        color = WDTTColors.terminalBlue.copy(alpha = if (isDark) 0.18f else 0.12f),
                        shape = RoundedCornerShape(topStart = 20.dp, topEnd = 20.dp),
                        modifier = Modifier.fillMaxWidth()
                    ) {
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 12.dp, vertical = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            if (pinnedStatsMessage != null) {
                                Text(
                                    text = pinnedStatsMessage
                                        .removePrefix("[СТАТИСТИКА] ")
                                        .removePrefix("[СТАТИСТИКА]"),
                                    color = WDTTColors.terminalBlue,
                                    fontSize = 12.sp,
                                    fontFamily = FontFamily.Monospace,
                                    fontWeight = FontWeight.SemiBold,
                                    lineHeight = 14.sp,
                                    maxLines = 1,
                                    softWrap = false,
                                    overflow = androidx.compose.ui.text.style.TextOverflow.Ellipsis,
                                    textAlign = TextAlign.Start,
                                    modifier = Modifier.weight(1f)
                                )
                            } else {
                                Spacer(modifier = Modifier.weight(1f))
                            }
                            if (uptimeText != null) {
                                Row(
                                    verticalAlignment = Alignment.CenterVertically,
                                    horizontalArrangement = Arrangement.spacedBy(4.dp)
                                ) {
                                    Icon(
                                        Icons.Default.Timer,
                                        contentDescription = null,
                                        tint = WDTTColors.terminalBlue,
                                        modifier = Modifier.size(14.dp)
                                    )
                                    Text(
                                        text = uptimeText,
                                        color = WDTTColors.terminalBlue,
                                        fontSize = 12.sp,
                                        fontFamily = FontFamily.Monospace,
                                        fontWeight = FontWeight.Bold,
                                        maxLines = 1
                                    )
                                }
                            }
                        }
                    }
                    HorizontalDivider(
                        color = WDTTColors.terminalBlue.copy(alpha = 0.35f),
                        thickness = 1.dp
                    )
                }

                AnimatedVisibility(
                    visible = pipelineState.visible,
                    enter = fadeIn() + expandVertically(),
                    exit = fadeOut() + shrinkVertically(),
                ) {
                    Column {
                        ConnectionPipelineCard(
                            state = pipelineState,
                            isDark = isDark,
                        )
                        HorizontalDivider(
                            color = WDTTColors.terminalBlue.copy(alpha = 0.20f),
                            thickness = 1.dp
                        )
                    }
                }

                if (scrollableLogs.isEmpty() && pinnedStatsMessage == null && uptimeText == null && !pipelineState.visible) {
                    Box(
                        modifier = Modifier.fillMaxSize().padding(32.dp),
                        contentAlignment = Alignment.Center
                    ) {
                        Column(
                            horizontalAlignment = Alignment.CenterHorizontally,
                            verticalArrangement = Arrangement.spacedBy(16.dp)
                        ) {
                            Icon(
                                imageVector = Icons.Filled.Notes,
                                contentDescription = null,
                                modifier = Modifier.size(64.dp),
                                tint = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.5f)
                            )
                            Text(
                                text = "Логи пусты",
                                style = MaterialTheme.typography.titleMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                textAlign = TextAlign.Center
                            )
                            Text(
                                text = "Здесь будут отображаться события туннеля",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.7f),
                                textAlign = TextAlign.Center
                            )
                        }
                    }
                } else {
                    LazyColumn(
                        state = listState,
                        modifier = Modifier
                            .fillMaxSize()
                            .padding(12.dp),
                        contentPadding = PaddingValues(bottom = 12.dp)
                    ) {
                        items(scrollableLogs, key = { it.key }) { entry ->
                            LogLine(entry)
                        }
                    }
                }
            }
        }
    }
}

@Composable
fun LogLine(entry: LogEntry) {
    val color = when {
        entry.isError -> WDTTColors.terminalRed
        entry.priority <= 2 -> WDTTColors.terminalGreen
        entry.priority == 3 -> WDTTColors.terminalBlue
        else -> WDTTColors.terminalText
    }

    var trigger by remember { mutableIntStateOf(0) }
    LaunchedEffect(entry.count) { trigger++ }

    val animatedScale by animateFloatAsState(
        targetValue = if (trigger > 0) 1.15f else 1.0f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy),
        label = "scale",
        finishedListener = { trigger = 0 }
    )

    val showDnsSettingsAction = entry.key == "go_dns_tip" ||
        entry.key == "err_vk_dns" ||
        entry.key == "go_dns_precheck_fail"

    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp)
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Surface(
                color = WDTTColors.terminalCounter.copy(alpha = 0.2f),
                shape = RoundedCornerShape(12.dp),
                modifier = Modifier
                    .defaultMinSize(minWidth = 24.dp, minHeight = 24.dp)
                    .graphicsLayer(scaleX = animatedScale, scaleY = animatedScale)
            ) {
                Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier.padding(horizontal = 6.dp)
                ) {
                    Text(
                        text = "${entry.count}",
                        color = WDTTColors.terminalBlue,
                        fontSize = 10.sp,
                        fontWeight = FontWeight.Bold,
                        maxLines = 1
                    )
                }
            }

            Spacer(modifier = Modifier.width(12.dp))

            Text(
                text = entry.message,
                color = color,
                fontSize = 13.sp,
                fontFamily = FontFamily.Monospace,
                fontWeight = if (entry.isError) FontWeight.Bold else FontWeight.Normal,
                lineHeight = 18.sp,
                modifier = Modifier.weight(1f)
            )
        }

        if (showDnsSettingsAction) {
            TextButton(
                onClick = { TunnelManager.requestOpenAppSettings() },
                contentPadding = PaddingValues(horizontal = 4.dp, vertical = 0.dp)
            ) {
                Text(
                    "Открыть ⚙️ → Сеть",
                    fontSize = 12.sp,
                    fontWeight = FontWeight.SemiBold
                )
            }
        }
    }
}
