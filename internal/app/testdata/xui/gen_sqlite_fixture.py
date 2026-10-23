# Generates sqlite_fixture.db and sqlite_fixture.json for TestSQLiteReaderMatchesSQLite.
# Run: python -I gen_sqlite_fixture.py  (from this directory)
import json, os, random, sqlite3

random.seed(7)
here = os.path.dirname(os.path.abspath(__file__))
db_path = os.path.join(here, "sqlite_fixture.db")
if os.path.exists(db_path):
    os.remove(db_path)
db = sqlite3.connect(db_path)
db.execute("PRAGMA page_size=1024")  # small pages: forces interior pages and overflow chains
db.execute("CREATE TABLE `items` (`id` integer PRIMARY KEY AUTOINCREMENT,`name` text,`n` integer,`f` real,`blob` blob,`flag` numeric)")
expected = []
for i in range(1, 121):
    size = random.choice([0, 10, 200, 1500, 3000])
    name = "row-%d-" % i + "x" * size + "中文"
    n = random.choice([0, 1, -1, 127, -128, 300, -70000, 2**31, -(2**40), 2**62, None])
    f = random.choice([0.5, -2.25, 1e10, None])
    blob = bytes([i % 256, 0, 255]) if i % 3 == 0 else None
    flag = i % 2
    db.execute("INSERT INTO items(name,n,f,blob,flag) VALUES (?,?,?,?,?)", (name, n, f, blob, flag))
db.execute("ALTER TABLE items ADD COLUMN `late` text DEFAULT 'x'")
db.execute("INSERT INTO items(name,n,f,blob,flag,late) VALUES ('after-alter',5,NULL,NULL,1,'set')")
db.execute("DELETE FROM items WHERE id % 7 = 0")
db.commit()
for row in db.execute("SELECT id,name,n,f,blob,flag,late FROM items ORDER BY id"):
    expected.append({"id": row[0], "name": row[1], "n": row[2], "f": row[3],
                     "blob": list(row[4]) if row[4] is not None else None, "flag": row[5], "late": row[6]})
db.execute("VACUUM")
db.close()
with open(os.path.join(here, "sqlite_fixture.json"), "w", encoding="utf-8") as out:
    json.dump(expected, out, ensure_ascii=False)
print(len(expected), "rows")
