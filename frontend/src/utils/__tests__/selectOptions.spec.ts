import { describe, expect, it } from 'vitest'
import { flattenGroupedSelectOptions } from '../selectOptions'

describe('flattenGroupedSelectOptions', () => {
  it('turns optgroups into disabled header options', () => {
    expect(
      flattenGroupedSelectOptions([
        {
          label: 'US',
          options: [{ value: 'us-east-1', label: 'us-east-1 (N. Virginia)' }],
        },
        {
          label: 'Europe',
          options: [{ value: 'eu-west-1', label: 'eu-west-1 (Ireland)' }],
        },
      ]),
    ).toEqual([
      { value: '__group:US', label: 'US', kind: 'group', disabled: true },
      { value: 'us-east-1', label: 'us-east-1 (N. Virginia)' },
      { value: '__group:Europe', label: 'Europe', kind: 'group', disabled: true },
      { value: 'eu-west-1', label: 'eu-west-1 (Ireland)' },
    ])
  })
})
