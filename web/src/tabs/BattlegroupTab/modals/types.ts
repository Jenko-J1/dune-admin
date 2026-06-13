import type { BackupFile } from '../../../api/client'
import type { ActionDef } from '../types'

export type CommandOutputModalProps = {
  runningCmd: string | null
  cmdOutput: string | null
  cmdDone: boolean
  lastBackupFile: string | null
  onClose: () => void
}

export type ConfirmDialogProps = {
  action: ActionDef | null
  /** Optional per-map target (e.g. "HaggaBasin (dune-server-survival-1)") shown
   *  in the body when the command targets a single container. */
  targetLabel?: string
  onConfirm: (a: ActionDef) => void
  onClose: () => void
}

export type RestoreModalProps = {
  open: boolean
  backupFiles: BackupFile[]
  backupFilesLoading: boolean
  setBackupFiles: (files: BackupFile[]) => void
  onClose: () => void
  onRestoreComplete: (output: string) => void
}
