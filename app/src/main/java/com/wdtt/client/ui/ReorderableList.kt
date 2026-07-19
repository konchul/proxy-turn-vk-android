package com.wdtt.client.ui

import androidx.compose.foundation.gestures.detectDragGesturesAfterLongPress
import androidx.compose.foundation.lazy.LazyListItemInfo
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput

fun Modifier.reorderable(
    state: LazyListState,
    onMove: (Int, Int) -> Unit,
    onDragEnd: () -> Unit = {}
): Modifier = this.then(Modifier.pointerInput(Unit) {
    var draggedDistance = 0f
    var draggingItemInitial: LazyListItemInfo? = null
    var draggingItemCurrent: LazyListItemInfo? = null

    detectDragGesturesAfterLongPress(
        onDragStart = { offset ->
            state.layoutInfo.visibleItemsInfo.firstOrNull { item ->
                offset.y.toInt() in item.offset..(item.offset + item.size)
            }?.also {
                draggingItemInitial = it
                draggingItemCurrent = it
            }
        },
        onDragCancel = {
            draggedDistance = 0f
            draggingItemInitial = null
            draggingItemCurrent = null
            onDragEnd()
        },
        onDragEnd = {
            draggedDistance = 0f
            draggingItemInitial = null
            draggingItemCurrent = null
            onDragEnd()
        },
        onDrag = { change, dragAmount ->
            change.consume()
            draggedDistance += dragAmount.y
            val initial = draggingItemInitial ?: return@detectDragGesturesAfterLongPress
            val current = draggingItemCurrent ?: return@detectDragGesturesAfterLongPress

            val startOffset = initial.offset + draggedDistance
            val endOffset = initial.offset + initial.size + draggedDistance

            val targetItem = state.layoutInfo.visibleItemsInfo.find { item ->
                item.index != current.index &&
                item.index != 0 && // Prevent swapping with the "Create Profile" button if it's index 0
                (startOffset < item.offset + item.size / 2 && endOffset > item.offset + item.size / 2)
            }

            if (targetItem != null) {
                onMove(current.index, targetItem.index)
                draggingItemCurrent = targetItem
                draggedDistance += initial.offset - targetItem.offset
                draggingItemInitial = targetItem
            }
        }
    )
})
