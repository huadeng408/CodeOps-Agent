# -*- coding: utf-8 -*-
import sqlite3, os, sys, datetime, json, collections
sys.stdout.reconfigure(encoding='utf-8')
DB = r'C:\Users\ieeep\.codex\logs_2.sqlite'
db = sqlite3.connect('file:' + DB.replace('\\', '/') + '?mode=ro', uri=True)
cur = db.cursor()
cur.execute('PRAGMA table_info(logs)')
for r in cur.fetchall():
    print(r)
cur.execute('SELECT * FROM logs LIMIT 2')
for r in cur.fetchall():
    print(r)
