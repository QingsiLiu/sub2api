export type GroupedSelectOption = {
  value: string | number | boolean | null
  label: string
  disabled?: boolean
  kind?: 'group'
}

export function flattenGroupedSelectOptions(
  groups: ReadonlyArray<{
    label: string
    options: ReadonlyArray<{ value: string; label: string }>
  }>
): GroupedSelectOption[] {
  return groups.flatMap((group) => [
    { value: `__group:${group.label}`, label: group.label, kind: 'group', disabled: true },
    ...group.options.map((option) => ({ value: option.value, label: option.label })),
  ])
}
