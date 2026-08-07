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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { getDefaultTimeRange } from '../utils'

describe('usage logs default time range', () => {
  test('covers the full current local day', () => {
    const { start, end } = getDefaultTimeRange()

    assert.equal(start.getFullYear(), end.getFullYear())
    assert.equal(start.getMonth(), end.getMonth())
    assert.equal(start.getDate(), end.getDate())
    assert.deepEqual(
      [
        start.getHours(),
        start.getMinutes(),
        start.getSeconds(),
        start.getMilliseconds(),
      ],
      [0, 0, 0, 0]
    )
    assert.deepEqual(
      [
        end.getHours(),
        end.getMinutes(),
        end.getSeconds(),
        end.getMilliseconds(),
      ],
      [23, 59, 59, 999]
    )
  })
})
