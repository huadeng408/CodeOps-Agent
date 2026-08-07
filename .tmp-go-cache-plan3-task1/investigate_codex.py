# -*- coding: utf-8 -*-
import sqlite3, os, datetime, sys
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\state_5.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(ms):
    return datetime.datetime.fromtimestamp(ms/1000).strftime('%Y-%m-%d %H:%M:%S') if ms else '-'
cur.execute('SELECT id, rollout_path, updated_at_ms, archived, title, cwd FROM threads')
rows = cur.fetchall()
missing = []
for r in rows:
    p2 = r[1].replace('\\', '/')
    if not os.path.exists(p2):
        missing.append((r[0], p2, ts(r[2]), r[3], (r[4] or '')[:30]))
print('threads:', len(rows), 'orphan (rollout file missing):', len(missing))
for m in missing[:60]:
    print(m[0][:26], '|', m[1][-90:], '|', m[2], '| arch:', m[3], '|', m[4])
# count orphans by archive status
import collections
c = collections.Counter(m[3] for m in missing)
print('orphans by archived:', dict(c))
