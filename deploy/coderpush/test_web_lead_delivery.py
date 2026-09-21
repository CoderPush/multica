import copy
import json
import unittest
from unittest.mock import patch
from web_lead_delivery import CHAT, LEAD, MARKER, PROJECT, WORKSPACE, now, open_db, process, validate, sweep, Lark

class FakeLark:
    def __init__(self):
        self.sent = 0
        self.fail = False
        self.bad_origin = False
    def check_origin(self, message):
        if self.bad_origin:
            raise ValueError('wrong channel')
    def reply(self, data, digest):
        self.sent += 1
        if self.fail:
            raise TimeoutError()
        return 'om_delivery123'

class DeliveryTest(unittest.TestCase):
    def setUp(self):
        self.db = open_db(':memory:')
        self.client = FakeLark()
        self.receipts = []
        self.data = {'reply_to':'om_origin12345', 'body':'PR #194 is resuming. No action needed from Anh.',
                     'milestone':'recovered', 'expected_status':'in_progress'}
        self.row = {'id':'comment-1', 'issue_id':'issue-1', 'workspace_id':WORKSPACE,
                    'project_id':PROJECT, 'author_id':LEAD, 'author_type':'agent',
                    'status':'in_progress', 'created_at':now(), 'content':MARKER+json.dumps(self.data)}
    def run_row(self, row=None, receipt=None):
        return process(row or self.row,self.db,lambda:self.client,receipt or (lambda issue,value:self.receipts.append(value)))
    def test_send_once_and_publish_once(self):
        self.assertEqual(self.run_row(),'sent')
        self.assertEqual(self.run_row(),'sent')
        self.assertEqual(self.client.sent,1)
        self.assertEqual(len(self.receipts),1)
        self.assertEqual(self.receipts[0]['chat_id'],CHAT)
    def test_duplicate_comment_does_not_send(self):
        self.run_row()
        row=dict(self.row,id='comment-2')
        self.assertEqual(self.run_row(row),'duplicate')
        self.assertEqual(self.client.sent,1)
    def test_receipt_failure_does_not_repeat_send(self):
        def fail(*args): raise RuntimeError('metadata unavailable')
        with self.assertRaises(RuntimeError): self.run_row(receipt=fail)
        self.assertEqual(self.run_row(),'sent')
        self.assertEqual(self.client.sent,1)
    def test_timeout_is_uncertain_never_retried(self):
        self.client.fail=True
        self.assertEqual(self.run_row(),'uncertain')
        self.assertEqual(self.run_row(),'uncertain')
        self.assertEqual(self.client.sent,1)
    def test_process_crash_never_retries_send(self):
        _,digest=validate(self.row)
        self.db.execute('INSERT INTO deliveries(comment_id,issue_id,digest,state,created_at) VALUES(?,?,?,?,?)',('comment-1','issue-1',digest,'sending',now()))
        self.db.commit()
        self.assertEqual(self.run_row(),'uncertain')
        self.assertEqual(self.client.sent,0)
        self.assertEqual(self.receipts[0]['state'],'uncertain')
    def test_scope_and_status_rejected(self):
        for key,value in [('workspace_id','other'),('project_id','other'),('author_id','other'),
                          ('author_type','member'),('status','done'),('created_at','2020-01-01T00:00:00+00:00')]:
            with self.subTest(key=key):
                row=dict(self.row,id=key,**{key:value})
                self.assertEqual(self.run_row(row),'rejected')
        self.assertEqual(self.client.sent,0)
    def test_wrong_origin_cannot_send(self):
        self.client.bad_origin=True
        self.assertEqual(self.run_row(), 'rejected')
        self.assertEqual(self.client.sent,0)
    def test_bad_newest_origin_does_not_starve_another_issue(self):
        class Selective(FakeLark):
            def check_origin(self, message):
                if message == 'om_origin12345': raise ValueError('invalid origin')
        client=Selective()
        data=dict(self.data,reply_to='om_valid12345')
        good=dict(self.row,id='good',issue_id='issue-2',content=MARKER+json.dumps(data))
        errors=sweep([self.row,good],self.db,lambda:client,lambda i,v:self.receipts.append(v))
        self.assertEqual(errors,[])
        self.assertEqual(client.sent,1)
        self.assertEqual([v['state'] for v in self.receipts],['rejected','sent'])
    def test_superseded_interrupted_send_recovers_and_holds_issue(self):
        self.db.execute('INSERT INTO deliveries(comment_id,issue_id,state,created_at) VALUES(?,?,?,?)',
                        ('old','issue-1','sending',now()))
        self.db.commit()
        errors=sweep([self.row],self.db,lambda:self.client,lambda i,v:self.receipts.append(v))
        self.assertEqual(errors,[])
        self.assertEqual(self.client.sent,0)
        self.assertEqual(self.receipts[0]['state'],'uncertain')
        self.assertEqual(self.db.execute("SELECT published FROM deliveries WHERE comment_id='old'").fetchone()[0],1)
    def test_old_successful_receipt_publishes_outside_query_window(self):
        value={'state':'sent','message_id':'om_old12345'}
        self.db.execute('INSERT INTO deliveries(comment_id,issue_id,state,receipt,created_at) VALUES(?,?,?,?,?)',
                        ('old','issue-1','sent',json.dumps(value),now()))
        self.db.commit()
        self.assertEqual(sweep([],self.db,lambda:self.client,lambda i,v:self.receipts.append(v)),[])
        self.assertEqual(self.receipts,[value])
        self.assertEqual(self.client.sent,0)

    def test_copied_origin_records_are_one_consistent_sender(self):
        client=Lark.__new__(Lark)
        with patch('web_lead_delivery.query',return_value=[{'sender':'ou_user123'},{'sender':'ou_user123'}]):
            client.check_origin('om_origin12345')
        for rows in [[],[{'sender':'ou_first'},{'sender':'ou_second'}],[{'sender':None}]]:
            with patch('web_lead_delivery.query',return_value=rows),self.assertRaises(ValueError):
                client.check_origin('om_origin12345')

    def test_bad_payload_rejected(self):
        for field,value in [('reply_to','https://evil.test'),('body',''),('body','x'*2401),
                            ('body','![secret](https://evil.test)'),('milestone','whatever')]:
            with self.subTest(field=field,value=value[:20]):
                data=copy.deepcopy(self.data);data[field]=value
                row=dict(self.row,id=field+str(len(value)),content=MARKER+json.dumps(data))
                self.assertEqual(self.run_row(row),'rejected')
        self.assertEqual(self.client.sent,0)

if __name__=='__main__': unittest.main()
