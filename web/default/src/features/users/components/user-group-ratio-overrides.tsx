/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { GroupBadge } from '@/components/group-badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { USER_GROUP_RATIO_OVERRIDE_LIMITS } from '../constants'
import { getUserGroupRatioOverrideEntries } from '../lib/group-ratio-overrides'

type UserGroupRatioOverridesProps = {
  value: Record<string, number>
  groups: string[]
  groupsLoaded: boolean
  onChange: (value: Record<string, number>) => void
}

export function UserGroupRatioOverrides(props: UserGroupRatioOverridesProps) {
  const { t } = useTranslation()
  const groupInputId = useId()
  const ratioInputId = useId()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingGroup, setEditingGroup] = useState<string | null>(null)
  const [draftGroup, setDraftGroup] = useState('')
  const [draftRatio, setDraftRatio] = useState('1')
  const [draftError, setDraftError] = useState<string | null>(null)

  const entries = getUserGroupRatioOverrideEntries(props.value)
  const configuredGroups = new Set(Object.keys(props.value))
  const knownGroups = new Set(props.groups)
  const availableGroupOptions = props.groups
    .filter((group) => !configuredGroups.has(group))
    .map((group) => ({ value: group, label: group }))
  const atEntryLimit =
    entries.length >= USER_GROUP_RATIO_OVERRIDE_LIMITS.MAX_ENTRIES

  const closeDialog = () => {
    setDialogOpen(false)
    setEditingGroup(null)
    setDraftGroup('')
    setDraftRatio('1')
    setDraftError(null)
  }

  const openCreateDialog = () => {
    setEditingGroup(null)
    setDraftGroup('')
    setDraftRatio('1')
    setDraftError(null)
    setDialogOpen(true)
  }

  const openEditDialog = (group: string, ratio: number) => {
    setEditingGroup(group)
    setDraftGroup(group)
    setDraftRatio(String(ratio))
    setDraftError(null)
    setDialogOpen(true)
  }

  const saveDraft = () => {
    const group = draftGroup.trim()
    const ratio = Number(draftRatio)

    if (!group) {
      setDraftError(t('Select a billing group'))
      return
    }
    if (!Number.isFinite(ratio) || ratio <= 0) {
      setDraftError(t('The multiplier must be greater than 0'))
      return
    }
    if (ratio > USER_GROUP_RATIO_OVERRIDE_LIMITS.MAX_RATIO) {
      setDraftError(t('The multiplier must not exceed 1000'))
      return
    }
    if (!editingGroup && configuredGroups.has(group)) {
      setDraftError(t('This billing group already has personal pricing'))
      return
    }

    props.onChange({ ...props.value, [group]: ratio })
    closeDialog()
  }

  const removeOverride = (group: string) => {
    const nextValue = { ...props.value }
    delete nextValue[group]
    props.onChange(nextValue)
  }

  return (
    <FieldSet className='gap-3 rounded-lg border p-3'>
      <FieldLegend variant='label'>{t('Personal group pricing')}</FieldLegend>
      <div className='flex items-start justify-between gap-3'>
        <FieldDescription className='min-w-0'>
          {t(
            "Override this user's multiplier for selected billing groups. It takes precedence over group price overrides, membership-group pricing, and base group pricing."
          )}
        </FieldDescription>
        <Button
          type='button'
          variant='outline'
          size='sm'
          onClick={openCreateDialog}
          disabled={
            !props.groupsLoaded ||
            atEntryLimit ||
            availableGroupOptions.length === 0
          }
        >
          {t('Add pricing rule')}
        </Button>
      </div>

      {entries.length === 0 ? (
        <div className='bg-muted/30 text-muted-foreground rounded-md border border-dashed px-3 py-4 text-center text-sm'>
          {t('No personal group pricing rules')}
        </div>
      ) : (
        <div className='divide-y rounded-md border'>
          {entries.map(([group, ratio]) => {
            const isUnavailable = props.groupsLoaded && !knownGroups.has(group)
            return (
              <div
                key={group}
                className='flex min-w-0 items-center justify-between gap-3 px-3 py-2.5'
              >
                <div className='min-w-0'>
                  <GroupBadge group={group} ratio={ratio} />
                  {isUnavailable && (
                    <p className='text-destructive mt-1 text-xs'>
                      {t('Billing group unavailable')}
                    </p>
                  )}
                </div>
                <div className='flex shrink-0 items-center gap-1'>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    onClick={() => openEditDialog(group, ratio)}
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    className='text-destructive hover:text-destructive'
                    onClick={() => removeOverride(group)}
                  >
                    {t('Delete')}
                  </Button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      <FieldDescription>
        {t('Pricing changes take effect after you save the user.')}
      </FieldDescription>

      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!open) closeDialog()
        }}
        title={
          editingGroup
            ? t('Edit personal group pricing')
            : t('Add personal group pricing')
        }
        description={t(
          'Set the multiplier applied when this user consumes the selected billing group.'
        )}
        footer={
          <>
            <Button type='button' variant='outline' onClick={closeDialog}>
              {t('Cancel')}
            </Button>
            <Button type='button' onClick={saveDraft}>
              {editingGroup ? t('Save changes') : t('Add pricing rule')}
            </Button>
          </>
        }
      >
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor={groupInputId}>{t('Billing group')}</FieldLabel>
            {editingGroup ? (
              <Input id={groupInputId} value={editingGroup} disabled />
            ) : (
              <Combobox
                id={groupInputId}
                options={availableGroupOptions}
                value={draftGroup}
                onValueChange={(value) => {
                  setDraftGroup(value ?? '')
                  setDraftError(null)
                }}
                placeholder={t('Select a billing group')}
                emptyText={t('No billing groups available')}
                listClassName='max-h-[min(360px,50dvh)]'
                openOnFocus
              />
            )}
          </Field>
          <Field>
            <FieldLabel htmlFor={ratioInputId}>{t('Multiplier')}</FieldLabel>
            <Input
              id={ratioInputId}
              type='number'
              min='0.001'
              max={USER_GROUP_RATIO_OVERRIDE_LIMITS.MAX_RATIO}
              step='0.001'
              inputMode='decimal'
              value={draftRatio}
              onChange={(event) => {
                setDraftRatio(event.target.value)
                setDraftError(null)
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault()
                  saveDraft()
                }
              }}
            />
            <FieldDescription>
              {t('For example, 0.8 means 20% off and 1 means no adjustment.')}
            </FieldDescription>
          </Field>
          <FieldError>{draftError}</FieldError>
        </FieldGroup>
      </Dialog>
    </FieldSet>
  )
}
