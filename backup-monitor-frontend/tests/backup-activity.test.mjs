import assert from 'node:assert/strict'
import test from 'node:test'
import { localBackupDurationByDay, summarizeLocalBackupActivity } from '../src/utils.ts'

const day = '2026-10-03'
const entry = (name, time, duration, kind = 'file', status = 'Successful', date = day) => ({ name, time, duration, kind, status, date })

test('today totals the same 26s site and 2s database runs as activity, not their file details', () => {
  const entries = [
    entry('Lần chạy sao lưu website', '05:00:01', 26, 'run', 'success'),
    entry('Lần chạy sao lưu cơ sở dữ liệu', '04:30:01', 2, 'run', 'success'),
    ...['web1.local', 'web2.local', 'web3.local', 'web4.local'].map((site, i) => entry(`Backup Website: ${site}`, '05:00:01', i === 0 ? 25 : 0.04)),
    ...['sql_web1_local', 'sql_web2_local', 'sql_web3_local', 'sql_web4_local'].map(db => entry(`Backup Database: ${db}`, '04:30:01', 0.04)),
  ]
  const rows = summarizeLocalBackupActivity(entries)
  assert.equal(rows.length, 2)
  assert.equal(rows[0].details.length, 4)
  assert.equal(rows[1].details.length, 4)
  assert.deepEqual(localBackupDurationByDay(entries, ['2026-10-02', day]), [
    { date: '2026-10-02', duration: null }, { date: day, duration: 28 },
  ])
})

test('legacy file totals, duplicate details, and missing duration remain honest', () => {
  const file = entry('Backup Website: shop.example', '05:00:01', '1.125')
  assert.deepEqual(localBackupDurationByDay([file, file, entry('Backup Website: other.example', '05:00:01', 0.25)], [day]), [{ date: day, duration: 1.375 }])
  assert.deepEqual(localBackupDurationByDay([file, entry('Backup Website: other.example', '05:00:01', null)], [day]), [{ date: day, duration: null }])
  assert.deepEqual(localBackupDurationByDay([entry('Backup Database: db', '04:30:01', 0)], [day]), [{ date: day, duration: 0 }])
})

test('manual and failed runs count separately, files inside a run do not count twice', () => {
  const entries = [
    entry('Lần chạy sao lưu website', '05:00:01', 26, 'run', 'success'),
    entry('Backup Website: shop.example', '05:00:20', 19),
    entry('Lần chạy sao lưu website', '08:00:01', 3, 'run', 'failed'),
    entry('Backup Website: shop.example', '08:00:01', 2),
    entry('Lần chạy sao lưu cơ sở dữ liệu', '08:01:01', '2', 'run', 'success'),
    entry('Lần chạy sao lưu website', '05:00:01', 99, 'run', 'success', '2026-09-01'),
    entry('Sao lưu cấu hình aaPanel', '05:10:01', 5, 'run', 'success'),
  ]
  assert.equal(summarizeLocalBackupActivity(entries).find(row => row.time === '05:00:01' && row.date === day).details.length, 1)
  assert.deepEqual(localBackupDurationByDay(entries, [day]), [{ date: day, duration: 31 }])
})

test('run without file details still appears, unknown duration is not zero', () => {
  assert.deepEqual(localBackupDurationByDay([entry('Lần chạy sao lưu website', '05:00:01', 26, 'run')], [day]), [{ date: day, duration: 26 }])
  for (const duration of [null, '', 'invalid', -1]) {
    assert.deepEqual(localBackupDurationByDay([entry('Lần chạy sao lưu website', '05:00:01', duration, 'run')], [day]), [{ date: day, duration: null }])
  }
})
