# -*- coding: utf-8 -*-
import sqlite3, os, datetime, sys
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\state_5.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(ms):
    return datetime.datetime.fromtimestamp(ms/1000).strftime('%Y-%m-%d %H:%M:%S') if ms else '-'
# threads for localcode dir
cur.execute("SELECT id, rollout_path, created_at_ms, updated_at_ms, archived, archived_at, title, source, model_provider, model, cwd FROM threads WHERE cwd LIKE '%localcode%' OR rollout_path LIKE '%localcode%' ORDER BY updated_at_ms DESC")
rows = cur.fetchall()
print('localcode threads:', len(rows))
for r in rows:
    print(ts(r[2]), '->', ts(r[3]), '| arch:', r[4], ts(r[5]), '|', r[7], '|', r[8], '|', r[9], '|', (r[6] or '')[:38], '|', r[1].replace('\\','/')[-55:])
print()
# distribution of model_provider for all threads
cur.execute('SELECT model_provider, archived, COUNT(*) FROM threads GROUP BY model_provider, archived')
print('provider x archived:', cur.fetchall())
