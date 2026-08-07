# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\state_5.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'

# threads with prefix 019fab98 or 019facf3
cur.execute("SELECT id, rollout_path, created_at_ms, updated_at_ms, archived, archived_at, title, source, model_provider FROM threads WHERE id LIKE '019fab98%' OR id LIKE '019facf3%' ORDER BY created_at_ms")
print('threads with prefix 019fab98 / 019facf3:')
for r in cur.fetchall():
    print(' ', r[0], '|', r[1].replace('\\','/')[-60:], '|', ts(r[2]/1000), '->', ts(r[3]/1000), '| arch:', r[4], ts(r[5]), '|', r[7][:20], '|', r[8], '|', (r[6] or '')[:30])

# which threads are in the 16:22 and 18:10 archive groups?
print()
for val, label in ((1785313346, 'Jul29 16:22:26'), (1785319805, 'Jul29 18:10:05'), (1785379038, 'Jul30 10:37:18'), (1785379040, 'Jul30 10:37:20')):
    cur.execute('SELECT id, title, cwd, rollout_path FROM threads WHERE archived_at = ?', (val,))
    rows = cur.fetchall()
    print(f'group {label} ({val}): {len(rows)} threads')
    for r in rows:
        print('   ', r[0], '|', (r[1] or '')[:40], '|', (r[2] or '')[:40], '|', r[3].replace('\\','/')[-50:])
