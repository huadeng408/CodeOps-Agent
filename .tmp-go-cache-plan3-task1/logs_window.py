# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'

# window around Jul 30 10:37:18 local
# epoch local: 1785379038 - 60 to + 300
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE ts BETWEEN 1785378978 AND 1785379400 ORDER BY ts LIMIT 200")
rows = cur.fetchall()
print(f'=== window Jul 30 10:36:18 - 10:43:20 (local) | {len(rows)} rows ===')
for r in rows:
    body = (r[3] or '')[:200].replace('\n', ' ')
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:45]}: {body}')
