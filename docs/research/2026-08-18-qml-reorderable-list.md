# QML reorderable list research (ticket #3)

Date: 2026-08-18  
Question: how to build a drag-reorderable list in hand-written QML/Quickshell, and what it costs.

## Findings

### What `ListView` does and does not provide

Qt's `ListView` has `move`, `moveDisplaced`, and `displaced` **transitions**. They animate delegates after the model reports an add/move/remove; they do not provide a drag handle, hit testing, insertion calculation, or model mutation. The `move` transition applies to the items that the model moved, while `moveDisplaced` applies to items displaced by that operation. [Qt `ListView` documentation](https://doc.qt.io/qt-6/qml-qtquick-listview.html#move-prop)

The smallest interaction therefore uses a `MouseArea` (or pointer handler) in each delegate, tracks press position and a small threshold, computes the destination index from pointer position, then calls a model mutation on release. `ListView` can animate the resulting move with `move`/`moveDisplaced`.

### `ListModel.move()` is the simplest QML-native model path

`ListModel.move(from, to, n)` moves `n` rows and emits the model change that a view needs. It is callable from QML/JavaScript, and Qt documents it as the direct operation for moving a contiguous range. [Qt `ListModel.move()`](https://doc.qt.io/qt-6/qml-qtqml-models-listmodel.html#move-method)

For a single-row reorder, the operation is usually `model.move(oldIndex, newIndex, 1)` with the destination adjusted when moving downward. This is materially smaller than removing and reinserting because it preserves the model's move notification and lets `ListView` use its move transitions.

`ListModel.get(index)` exposes row data to JavaScript, but Qt warns that returned objects are not stable across model modifications and must not be retained in bindings or across changes. Use stable IDs and re-read by index after a move. [Qt `ListModel.get()`](https://doc.qt.io/qt-6/qml-qtqml-models-listmodel.html#get-method)

### `DelegateModel` is not required for ordinary reorder

Qt says `DelegateModel` is usually unnecessary. It is useful for accessing `modelIndex` with a `QAbstractItemModel`, and for grouping/filtering delegate items; it encapsulates a source model and delegate rather than replacing the source model's mutation API. [Qt `DelegateModel` detailed description](https://doc.qt.io/qt-6/qml-qtqml-models-delegatemodel.html#details)

Use `DelegateModel` when the list needs filtered/sorted groups or model-index mapping. For a single editable ordered list backed by `ListModel`, it adds complexity without providing the reorder operation.

### JS-backed models and persistent configuration

An ordinary JavaScript array is not a QML model: changing `array.splice()` does not produce the `rowsMoved`/change notifications that `ListView` consumes. A JS-backed source therefore needs either (a) a `ListModel` mirror that is mutated with `move()`, or (b) an explicit rebuild/reset after mutating the array, or (c) a host-side mutation API that persists the array/config and causes the view to reload.

Omarchy's first-party bar takes option (c). In [`shell/plugins/bar/Bar.qml`](https://github.com/basecamp/omarchy/blob/master/shell/plugins/bar/Bar.qml), each module has a full-size `MouseArea`; it records press coordinates, waits for a 4-space drag threshold, finds a target with `moduleDropAtScene()`, and on release calls `dropBarModuleAtTarget()`. The target calculation is delegated to `BarModel.nearestDropTarget()`.

The actual reorder mutates the persistent JSON layout, not delegate positions: `moveModuleInConfig()` removes the source entry with `splice()`, adjusts the destination when moving within one region, and inserts it into the target region; `dropBarModule()` wraps this in `shell.mutateShellConfig()`. The source explicitly avoids `drag.target` because modules are owned by `Row`/`Column` positioners and direct x/y mutation can leave stale offsets and overlap. [Omarchy `Bar.qml` source](https://github.com/basecamp/omarchy/blob/master/shell/plugins/bar/Bar.qml#L1650-L1740), [reorder implementation](https://github.com/basecamp/omarchy/blob/master/shell/plugins/bar/Bar.qml#L630-L750)

This is direct evidence that a JS/config-backed list can be reorderable, but the persistence and layout synchronization are the hard parts, not `ListView` itself. Omarchy's bar does not use `DelegateModel` for this workflow.

## Smallest working technique

For a purely in-memory editor:

```qml
ListModel { id: rows }

ListView {
    model: rows
    delegate: Item {
        required property int index
        MouseArea {
            anchors.fill: parent
            property real pressY
            onPressed: pressY = mouse.y
            onReleased: {
                const destination = Math.max(0, Math.min(rows.count - 1,
                    index + Math.round((mouse.y - pressY) / height)))
                if (destination !== index)
                    rows.move(index, destination, 1)
            }
        }
    }
    move: Transition { NumberAnimation { properties: "x,y"; duration: 120 } }
    moveDisplaced: Transition { NumberAnimation { properties: "x,y"; duration: 120 } }
}
```

Production behavior needs a real drag state, pointer tracking while pressed, insertion-marker/drop-target math, click suppression, and persistence. For a config-backed list, copy Omarchy's pattern: keep layout ownership in the source config, compute a target from geometry, mutate the source array/config on release, then let the layout recreate/reposition delegates.

## Rough cost

* **In-memory `ListModel` reorder:** about 25–50 QML lines for a basic vertical list; 60–100 lines for threshold, live target feedback, click suppression, and animation. Complexity: low.
* **Persistent JS/config-backed reorder:** about 100–180 lines including drag state, geometry/drop target calculation, ghost/marker feedback, config mutation, and reload synchronization. Complexity: medium; persistence and positioner ownership are the main risks.
* **`DelegateModel`:** no reduction for the basic case; likely increases code unless filtering/grouping or `modelIndex` access is already needed.

## Decision

Reorder should remain out of v1 unless editing order becomes a requirement. If revisited, start with a `ListModel` and `move()` for the smallest implementation. If the real model is a JS/config array, treat the array/config as the source of truth and implement explicit drag/drop mutation in the Omarchy style; do not depend on changing delegate x/y or on `DelegateModel` alone.

