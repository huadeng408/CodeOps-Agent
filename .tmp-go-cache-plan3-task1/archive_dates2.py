# -*- coding: utf-8 -*-
import sqlite3, os, datetime, sys
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\state_5.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'
print('sample conversions:')
for v in (1785379038, 1785319805, 1785313346, 1773923954, 1782108831):
    print(' ', v, '=', ts(v))

print()
cur.execute("SELECT id, title, archived, archived_at, updated_at_ms FROM threads WHERE id IN ('019fab95-5593-7370-b218-808e12500831','019eab14-5594-7171-a399-0dd74525ab8c','019e8871-1e27-7a73-8ce0-c74b219a4fab','019e886f-f8b3-7863-9a62-5e7b7e3fb091','019e451b-36de-7803-8172-aa29d2e438b3','019e4080-0c58-7f30-bc74-9d70a3b32589')")
print('localcode archived threads:')
for r in cur.fetchall():
    print(' ', r[0][:24], '|', (r[1] or '')[:30], '| arch:', r[2], '| archived_at:', r[3], ts(r[3]), '| updated:', ts(r[4]/1000))

cur.execute("PRAGMA table_info(_sqlx_migrations)")
cols = [c[1] for c in cur.fetchall()]
print()
print('migrations cols:', cols)
cur.execute("SELECT * FROM _sqlx_migrations ORDER BY rowid DESC LIMIT 8")
for r in cur.fetchall():
    print(' ', r)
