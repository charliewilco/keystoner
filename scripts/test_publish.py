import hashlib
import json
import pathlib
import sqlite3
import tempfile
import unittest
from publish import publish, validate

class PublisherTests(unittest.TestCase):
    def test_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            m=publish('seed/catalog.sql',tmp,'test-1','2026-11-09T22:00:00Z')
            p=pathlib.Path(tmp)/'releases/test-1/catalog.sqlite'
            self.assertEqual(hashlib.sha256(p.read_bytes()).hexdigest(),m['sha256'])
            self.assertEqual(p.stat().st_size,m['byte_size'])
            with sqlite3.connect(p) as db:
                self.assertEqual(db.execute('PRAGMA user_version').fetchone()[0],1)
                self.assertEqual(db.execute('SELECT count(*) FROM resources').fetchone()[0],108)
                self.assertEqual(db.execute('SELECT id FROM resources WHERE benefit_id=1').fetchone()[0],'ks-000001')
                db.execute('DELETE FROM benefit_sources WHERE benefit_id=1')
                with self.assertRaisesRegex(ValueError,'unsourced'): validate(db)
            with self.assertRaisesRegex(ValueError,'already exists'):
                publish('seed/catalog.sql',tmp,'test-1','2026-11-09T22:00:00Z')
    def test_bad_seed_and_id_leave_no_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(ValueError):publish('seed/catalog.sql',tmp,'../bad','2026-11-09T22:00:00Z')
            seed=pathlib.Path(tmp)/'bad.sql'
            seed.write_text(pathlib.Path('seed/catalog.sql').read_text()+"\nDELETE FROM benefit_details WHERE benefit_id=1;")
            with self.assertRaisesRegex(ValueError,'missing cost'):publish(seed,tmp,'bad','2026-11-09T22:00:00Z')
            self.assertFalse((pathlib.Path(tmp)/'manifest.json').exists())
    def test_reproducible(self):
        with tempfile.TemporaryDirectory() as a,tempfile.TemporaryDirectory() as b:
            self.assertEqual(publish('seed/catalog.sql',a,'r1','2026-11-09T22:00:00Z'),publish('seed/catalog.sql',b,'r1','2026-11-09T22:00:00Z'))
if __name__=='__main__':unittest.main()
