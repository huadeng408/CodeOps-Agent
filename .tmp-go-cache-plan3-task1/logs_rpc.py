# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%m-%d %H:%M:%S') if sec else '-'

# all app_server RPC methods seen in logs
cur.execute("SELECT DISTINCT rpc_method FROM (SELECT substr(feedback_log_body, instr(feedback_log_body,'rpc.method=')+11, 60) AS rpc_method FROM logs) LIMIT 200")
# simpler: grep rpc.method= values
cur.execute("SELECT feedback_log_body FROM logs WHERE feedback_log_body LIKE '%rpc.method=%' AND feedback_log_body LIKE '%thread/%' LIMIT 0")
cur.execute("SELECT ts, level, feedback_log_body FROM logs WHERE feedback_log_body LIKE '%rpc.method=%' AND ts BETWEEN 1785319800 AND 1785400000 ORDER BY ts LIMIT 120")
rows = cur.fetchall()
print(f'=== app-server RPC Jul29 18:10 - Jul30 ~12:00 ({len(rows)}) ===')
import re
for r in rows:
    m = re.search(r'rpc\.method=(\S+)', r[2] or '')
    body = (r[2] or '')[:130].replace('\n', ' ')
    print(f'  {ts(r[0])} [{r[1]}] {m.group(1) if m else "?"}: {body}')
