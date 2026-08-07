# -*- coding: utf-8 -*-
import sqlite3, os, sys, datetime, json
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
cur.execute("SELECT name FROM sqlite_master WHERE type='table'")
tables = [r[0] for r in cur.fetchall()]
print('tables:', tables)
for t in tables:
    try:
        cur.execute('SELECT COUNT(*) FROM ' + t)
        print(' ', t, cur.fetchone()[0])
    except Exception as e:
        print(' ', t, 'ERR', e)
