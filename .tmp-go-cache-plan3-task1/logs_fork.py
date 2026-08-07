# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%m-%d %H:%M:%S') if sec else '-'

# fork event details
cur.execute("SELECT ts, level, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%thread/fork%' ORDER BY ts")
rows = cur.fetchall()
print(f'=== thread/fork events ({len(rows)}) ===')
for r in rows:
    print(f'  {ts(r[0])} [{r[1]}]: {(r[2] or "")[:400]}')

# thread/start events at 08:28:54 Jul 30 - full body of a few
print()
cur.execute("SELECT ts, level, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%rpc.method=%thread/start%' AND ts BETWEEN 1785360000 AND 1785360600 LIMIT 5")
for r in cur.fetchall():
    print(f'  {ts(r[0])} [{r[1]}]: {(r[2] or "")[:600]}')
