# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%m-%d %H:%M:%S') if sec else '-'

# 019fab98-710c events from Jul 30 10:37 to Aug 1
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%019fab98-710c%' AND ts BETWEEN 1785379200 AND 1785540000 ORDER BY ts LIMIT 80")
rows = cur.fetchall()
print(f'=== 019fab98-710c events Jul30 10:40 - Jul31 ~14:00 ({len(rows)}) ===')
for r in rows:
    body = (r[3] or '')[:180].replace('\n', ' ')
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:40]}: {body}')
