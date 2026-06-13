import type { TFunction } from 'i18next'
import type { Column } from '../../dune-ui'

export type ServersTableProps = {
  servers: ServerRow[]
  isInitializing: boolean
  emptyMessage?: string
  /** When true (and onServerAction is set), an Actions column with per-map
   *  Start/Restart/Stop buttons is shown (docker fleet only). */
  canControl?: boolean
  /** True while any command is in flight — disables the per-row buttons. */
  busy?: boolean
  /** Invoked when a per-map lifecycle button is pressed. */
  onServerAction?: (server: ServerRow, action: ActionDef) => void
}

export type ServerSortKey = 'map' | 'phase' | 'players' | 'queue' | 'ready' | 'dimension' | 'partition' | 'age' | 'actions'

export type ServerRow = {
  map: string
  sietch: string
  dimension: number
  partition: number
  phase: string
  ready: boolean
  players: number
  playerHardCap: number
  queue: number
  port?: number
  ageSeconds?: number
  /** Per-map container name (docker fleet only) — stable row key + action target. */
  container?: string
  /** Managed by the dune-autoscaler (docker fleet only) — a Stop may be undone. */
  autoscaled?: boolean
}

export type BGInfo = {
  name: string
  title: string
  phase: string
  database: string
}

export type DetailedStatus = {
  battlegroup: BGInfo
  servers: ServerRow[]
}

export type ActionDef = {
  label: string
  cmd: string
  danger: boolean
  msg: string
}

export function getServerColumns(t: TFunction, withActions = false): Column<ServerSortKey>[] {
  const cols: Column<ServerSortKey>[] = [
    { key: 'map', label: t('battlegroup.columns.map'), isRowHeader: true },
    { key: 'phase', label: t('battlegroup.columns.phase'), width: 100 },
    { key: 'players', label: t('battlegroup.columns.players'), width: 80 },
    { key: 'queue', label: t('battlegroup.columns.queue'), width: 70 },
    { key: 'ready', label: t('battlegroup.columns.ready'), width: 70 },
    { key: 'dimension', label: t('battlegroup.columns.dim'), width: 60 },
    { key: 'partition', label: t('battlegroup.columns.part'), width: 60 },
    { key: 'age', label: t('battlegroup.columns.age'), width: 80 },
  ]
  if (withActions) {
    cols.push({ key: 'actions', label: t('battlegroup.columns.actions'), width: 124, sortable: false, align: 'end' })
  }
  return cols
}

export const ACTIONS: ActionDef[] = [
  { label: 'start', cmd: 'start', danger: false, msg: 'startMsg' },
  { label: 'stop', cmd: 'stop', danger: true, msg: 'stopMsg' },
  { label: 'restart', cmd: 'restart', danger: false, msg: 'restartMsg' },
  { label: 'update', cmd: 'update', danger: false, msg: 'updateMsg' },
  { label: 'backup', cmd: 'backup', danger: false, msg: 'backupMsg' },
]

// SERVER_ACTIONS are the per-map (single container) lifecycle commands. Update
// and backup are battlegroup-wide only, so they are not offered per row.
export const SERVER_ACTIONS: ActionDef[] = [
  { label: 'start', cmd: 'start', danger: false, msg: 'startMsg' },
  { label: 'restart', cmd: 'restart', danger: false, msg: 'restartMsg' },
  { label: 'stop', cmd: 'stop', danger: true, msg: 'stopMsg' },
]

export const INIT_WARN_MS = 3 * 60 * 1000

export type ChipColor = 'default' | 'success' | 'warning' | 'danger'
