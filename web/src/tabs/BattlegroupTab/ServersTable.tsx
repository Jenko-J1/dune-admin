import * as React from 'react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@heroui-pro/react'
import { Button } from '@heroui/react'
import { DataTable, Icon } from '../../dune-ui'
import { phaseColor } from './helpers'
import { formatUptime } from './uptime'
import { getServerColumns, SERVER_ACTIONS, type ServerRow, type ServerSortKey, type ServersTableProps } from './types'

// Lucide icon per per-map lifecycle command.
const SERVER_ACTION_ICON: Record<string, string> = {
  start: 'play',
  restart: 'rotate-cw',
  stop: 'square',
}

// actionDisabled reflects container state so a Start on a running map (or a Stop
// on a stopped one) is greyed out; restart is always available while not busy.
function actionDisabled(cmd: string, s: ServerRow, busy: boolean): boolean {
  if (busy) return true
  if (cmd === 'start') return s.ready
  if (cmd === 'stop') return !s.ready
  return false
}

export const ServersTable: React.FC<ServersTableProps> = ({ servers, isInitializing, emptyMessage, canControl, busy, onServerAction }) => {
  const { t } = useTranslation()
  const withActions = !!canControl && !!onServerAction
  return (
    <DataTable<ServerRow, ServerSortKey>
      aria-label={t('nav.battlegroup')}
      className="min-h-0 max-h-full"
      columns={getServerColumns(t, withActions)}
      rows={servers}
      rowId={(s) => s.container ?? `${s.map}-${s.dimension}-${s.partition}`}
      initialSort={{ column: 'map', direction: 'ascending' }}
      sortValue={(r, k) => {
        if (k === 'actions') return 0
        if (k === 'ready') return r.ready ? 1 : 0
        if (k === 'age') return r.ageSeconds ?? 0
        return r[k] as string | number
      }}
      emptyState={emptyMessage && (
        <EmptyState size="sm">
          <EmptyState.Header>
            <EmptyState.Title>{emptyMessage}</EmptyState.Title>
          </EmptyState.Header>
        </EmptyState>
      )}
      renderCell={(s, key) => {
        switch (key) {
          case 'map':
            return (
              <span className="flex items-center gap-1.5">
                <span className="font-mono">{s.map}</span>
                {s.autoscaled && (
                  <span
                    title={t('battlegroup.autoscaledTip')}
                    className="rounded bg-warning/15 px-1 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-warning"
                  >
                    {t('battlegroup.autoscaled')}
                  </span>
                )}
              </span>
            )
          case 'phase':
            return (
              <span className="font-semibold" style={{ color: phaseColor(s.phase) }}>
                {s.phase || '—'}
                {isInitializing && s.phase === 'Running' && (
                  <span className="ml-1 font-normal text-warning">{t('battlegroup.initializing')}</span>
                )}
              </span>
            )
          case 'players':
            return (
              <span className="font-semibold" style={{ color: s.players > 0 ? 'var(--success)' : 'var(--muted)' }}>
                {s.players}
                {s.playerHardCap > 0 && (
                  <span className="font-normal text-muted">{`/${s.playerHardCap}`}</span>
                )}
              </span>
            )
          case 'queue':
            return (
              <span style={{ color: s.queue > 0 ? 'var(--warning)' : 'var(--muted)' }}>
                {s.queue}
              </span>
            )
          case 'ready':
            return (
              <Icon
                name={s.ready ? 'check' : 'x'}
                className={`size-4 ${s.ready ? 'text-success' : 'text-danger'}`}
              />
            )
          case 'dimension': return <span className="text-muted">{s.dimension}</span>
          case 'partition': return <span className="text-muted">{s.partition}</span>
          case 'age': return <span className="font-mono text-muted">{formatUptime(s.ageSeconds)}</span>
          case 'actions':
            if (!withActions || !s.container) return null
            return (
              <div className="flex items-center justify-end gap-1">
                {SERVER_ACTIONS.map((action) => (
                  <Button
                    key={action.cmd}
                    size="sm"
                    variant={action.danger ? 'danger-soft' : 'ghost'}
                    className="px-2 min-w-0"
                    isDisabled={actionDisabled(action.cmd, s, !!busy)}
                    aria-label={`${t(`battlegroup.actions.${action.cmd}` as never)} ${s.map}`}
                    onPress={() => onServerAction?.(s, action)}
                  >
                    <Icon name={SERVER_ACTION_ICON[action.cmd]} />
                  </Button>
                ))}
              </div>
            )
        }
      }}
    />
  )
}
