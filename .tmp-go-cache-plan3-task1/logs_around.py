# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'

# what happened just before 10:37 on Jul 30 (10:00-10:37)
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE ts BETWEEN 1785376800 AND 1785379040 AND level IN ('WARN','ERROR') ORDER BY ts LIMIT 60")
rows = cur.fetchall()
print(f'=== Jul 30 10:00-10:37 WARN/ERROR ({len(rows)}) ===')
for r in rows:
    body = (r[3] or '')[:220].replace('\n', ' ')
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:45]}: {body}')

# any log referencing 019fab98 between Jul 30 and Aug 1
print()
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%019fab98%' ORDER BY ts LIMIT 40")
rows = cur.fetchall()
print(f'=== logs mentioning 019fab98 (first 40) ===')
for r in rows:
    body = (r[3] or '')[:200].replace('\n', ' ')
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:40]}: {body}')
