import test from 'node:test'
import assert from 'node:assert/strict'
import { resultBasisLabel } from '../src/platform/learning-results.ts'

test('original assets do not inherit analysis or approval claims', () => {
  const original = resultBasisLabel('created')
  assert.match(original, /原创收录/)
  assert.match(original, /未核验/)
  assert.doesNotMatch(original, /自动分析|已审核|已批准/)
})

test('known evidence remains distinct and unknown basis does not imply analysis', () => {
  assert.match(resultBasisLabel('measured'), /测量.*待核验/)
  assert.match(resultBasisLabel('inferred'), /分析推断.*待核验/)
  for (const basis of ['', 'future-kind', 'constructor', '__proto__']) {
    assert.equal(resultBasisLabel(basis), '来源未知 · 待补充来源依据')
  }
})
