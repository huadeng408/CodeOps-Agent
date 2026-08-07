# -*- coding: utf-8 -*-
import sqlite3, sys, datetime
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
def ts(sec):
    return datetime.datetime.fromtimestamp(sec).strftime('%m-%d %H:%M:%S') if sec else '-'
# around the 09:36:10 Jul 29 archive (epoch: 1785296170)
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE ts BETWEEN 1785295950 AND 1785296250 ORDER BY ts LIMIT 60")
for r in cur.fetchall():
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:40]}: {(r[3] or "")[:160]}')
print()
# around 09:58:36 Jul 29 (1785297516)
cur.execute("SELECT ts, level, target, feedback_log_body FROM logs WHERE ts BETWEEN 1785297450 AND 1785297600 ORDER BY ts LIMIT 40")
for r in cur.fetchall():
    print(f'  {ts(r[0])} [{r[1]}] {r[2][:40]}: {(r[3] or "")[:160]}')
