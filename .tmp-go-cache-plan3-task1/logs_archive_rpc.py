# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%Y-%m-%d %H:%M:%S') if sec else '-'

print('=== all thread/archive RPC calls ===')
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%thread/archive%' ORDER BY ts")
rows = cur.fetchall()
print('count:', len(rows))
for r in rows:
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:40]}: {(r[3] or "")[:250]}')

print()
print('=== thread/delete RPC ===')
cur.execute("SELECT ts, level, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%thread/delete%' ORDER BY ts")
rows = cur.fetchall()
print('count:', len(rows))
for r in rows[:10]:
    print(f'  {ts(r[0])} [{r[1]}]: {(r[2] or "")[:250]}')
