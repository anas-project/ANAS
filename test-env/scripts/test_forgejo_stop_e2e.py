import importlib.util
import json
from pathlib import Path
import sqlite3
import unittest

spec=importlib.util.spec_from_file_location('forgejo_stop',Path(__file__).with_name('server-forgejo-stop-e2e.py'))
native=importlib.util.module_from_spec(spec);spec.loader.exec_module(native)


class StopIntegrationGuards(unittest.TestCase):
    def test_registration_cleanup_counts_live_rows_not_retained_tombstones(self):
        with sqlite3.connect(':memory:') as db:
            db.execute('CREATE TABLE action_runner (id INTEGER PRIMARY KEY, deleted INTEGER)')
            self.assertEqual(native.active_registration_count(db),0)
            db.executemany('INSERT INTO action_runner VALUES (?,?)',[(1,0),(2,1790317748)])
            self.assertEqual(native.active_registration_count(db),1)
            before=db.execute('SELECT id,deleted FROM action_runner ORDER BY id').fetchall()
            native.active_registration_count(db)
            self.assertEqual(db.execute('SELECT id,deleted FROM action_runner ORDER BY id').fetchall(),before)
            db.execute('DELETE FROM action_runner WHERE id=1')
            self.assertEqual(native.active_registration_count(db),0)

    def test_missing_or_ambiguous_registration_deletion_facts_are_not_empty(self):
        for row in ((1,None),(1,-1),(1,'retired'),(0,1)):
            with sqlite3.connect(':memory:') as db:
                db.execute('CREATE TABLE action_runner (id,deleted)')
                db.execute('INSERT INTO action_runner VALUES (?,?)',row)
                with self.assertRaises(native.GateFailure):native.active_registration_count(db)
        with sqlite3.connect(':memory:') as db:
            db.execute('CREATE TABLE action_runner (id INTEGER PRIMARY KEY)')
            with self.assertRaises(native.GateFailure):native.active_registration_count(db)

    def test_forwarding_snapshot_does_not_claim_connectivity_or_accept_partial_input(self):
        chain={'family':'ip','table':'filter','name':'FORWARD','type':'filter','hook':'forward','policy':'drop'}
        for policy in ('accept','drop'):
            observation=native.forwarding_observation({'nftables':[{'chain':{**chain,'policy':policy}}]})
            self.assertEqual(observation['policy'],policy)
            self.assertNotIn('ready',observation)
            self.assertNotIn('passed',observation)
            self.assertIn('neither connectivity',observation['scope'])
        for value in (None,[],{}, {'nftables':[]},{'nftables':[None]},
                      {'nftables':[{'chain':chain},{'chain':chain}]},
                      {'nftables':[{'chain':{**chain,'policy':None}}]},
                      {'nftables':[{'chain':{**chain,'family':'ip6'}}]},
                      {'nftables':[{'chain':{**chain,'hook':'input'}}]}):
            with self.assertRaises(native.GateFailure):native.forwarding_observation(value)

    def core_events(self):
        package='github.com/anas-project/ANAS/internal/runner'
        name='TestNativeForgejoCoreStop'
        return [{'Action':'start','Package':package},
                {'Action':'run','Package':package,'Test':name},
                {'Action':'pass','Package':package,'Test':name},
                {'Action':'pass','Package':package}]

    def test_core_stop_requires_actual_run_and_unique_package_completion(self):
        events=self.core_events()
        self.assertTrue(native.core_events_passed(events,0))
        for changed in ([events[2]], events[:-1], events+[events[-1]],
                        [events[0],events[2],events[1],events[3]],
                        [{**row,'Package':'unrelated/package'} for row in events],
                        events+[{'Action':'pass','Package':events[0]['Package'],'Test':'MockCoreStop'}]):
            with self.subTest(events=changed):
                self.assertFalse(native.core_events_passed(changed,0))
        for code in (False,True,None,'0',1,-9):
            self.assertFalse(native.core_events_passed(events,code))

    def test_core_stop_rejects_malformed_or_incomplete_evidence(self):
        events=self.core_events()
        for changed in (None,[],{},'not-events',events+[None],events+[[]],events+[{}],
                        events+[{'Action':'skip','Package':events[0]['Package']}],
                        [events[0],{'Action':'pass','Package':events[0]['Package'],'Test':[]},*events[1:]]):
            with self.subTest(events=changed):
                self.assertFalse(native.core_events_passed(changed,0))

    def test_engine_observer_only_selects_this_frozen_managed_guest(self):
        instance={'name':'anas-fj-'+'a'*20,'type':'container','status':'Running',
                  'config':{'user.anas.managed':'true','user.anas.workload':'fixture-job',
                            'volatile.base_image':native.IMAGE_PIN,'volatile.uuid':'00000000-0000-4000-8000-000000000001'}}
        self.assertTrue(native.observable_guest(instance))
        for changed in ({**instance,'name':'other-guest'}, {**instance,'type':'virtual-machine'},
                        {**instance,'status':'Stopped'}, {**instance,'config':{}},
                        {**instance,'name':None}, {**instance,'config':None},
                        {**instance,'config':{**instance['config'],'volatile.uuid':None}},
                        {**instance,'config':{**instance['config'],'user.anas.workload':True}},
                        {**instance,'config':{**instance['config'],'volatile.base_image':'b'*64}},
                        {**instance,'config':{**instance['config'],'user.anas.managed':'false'}}):
            self.assertFalse(native.observable_guest(changed))
        for command in native.GUEST_BOOT_OBSERVATIONS:
            self.assertIn(command[0],('/usr/bin/systemctl','/usr/bin/journalctl','/usr/bin/stat'))
            self.assertNotIn('/usr/bin/podman', command)
            self.assertFalse(any(item in command for item in ('start','restart','stop','enable','set','daemon-reload')))

    def test_workflow_uses_the_pinned_images_shell_and_fixed_cases(self):
        for delay in (0, 240):
            source=native.workflow_source(delay)
            self.assertIn('on: [push]\n',source)
            self.assertIn('shell: sh\n',source)
            self.assertIn('sleep '+str(delay)+'\n',source)
            self.assertIn('test ! -e /run/anas-actions-token/runner-token',source)
        for delay in (True, -1, 1, '240', '0; echo unsafe'):
            with self.assertRaises(native.GateFailure): native.workflow_source(delay)

    def test_transport_contains_local_git_protocol_and_hook_programs(self):
        installed = dict(native.TRANSPORT_PROGRAMS)
        for path in ('/usr/bin/git', '/usr/bin/git-upload-pack', '/usr/bin/git-receive-pack',
                     '/usr/bin/git-upload-archive', '/usr/bin/env', '/usr/bin/cat', '/usr/bin/basename',
                     '/usr/bin/dirname', '/bin/sh', '/bin/bash', '/usr/bin/incus'):
            self.assertEqual(installed.get(path), path)
        self.assertEqual(len(installed), len(native.TRANSPORT_PROGRAMS))
        for source, destination in native.TRANSPORT_PROGRAMS:
            self.assertTrue(Path(source).is_absolute())
            self.assertEqual(source, destination)

    def test_generation_changes_only_the_single_actions_flag(self):
        source='APP_NAME = Fixture\n[oauth2]\nJWT_SECRET = existing-generated-value\n[actions]\nENABLED   = true\n[service]\nENABLED = false\n'
        changed=native.application_configuration(source,False)
        self.assertEqual(changed,source.replace('ENABLED   = true','ENABLED   = false'))
        self.assertEqual(native.application_configuration(changed,True),source)
        for invalid in ('[actions]\n', '[actions]\nENABLED = true\nENABLED = false\n', source+'[actions]\nENABLED = true\n'):
            with self.assertRaises(native.GateFailure): native.application_configuration(invalid,False)

    def test_supply_keeps_canonical_compact_encoding_and_final_newline(self):
        descriptor={'version':'anas.compute-image-supply/v1','images':[{'resolution':{'fingerprint':'a'*64},'release':{'version':'fixture'}}]}
        expected=b'{"version":"anas.compute-image-supply/v1","images":[{"resolution":{"fingerprint":"'+b'a'*64+b'"},"release":{"version":"fixture"}}]}\n'
        self.assertEqual(native.supply_bytes(descriptor),expected)
        self.assertNotEqual(native.supply_bytes(descriptor),json.dumps(descriptor).encode())

    def test_exact_disposable_vm_and_empty_daemon_only(self):
        identity='anas-incus-host-abcdef'
        facts={'uid':0,'vendor':'QEMU','instance':identity,'docker_root':'/var/lib/anas-host-provision-test','containers':[]}
        self.assertTrue(native.valid_environment(identity,facts))
        for key,value in (('uid',1000),('vendor','physical'),('instance',identity+'1'),('docker_root','/var/lib/docker'),('containers',['business'])):
            self.assertFalse(native.valid_environment(identity,{**facts,key:value}))
        self.assertFalse(native.valid_environment(identity+'\n',facts))

    def test_exact_unique_complete_lifecycle_required(self):
        events=[{'stage':name,'status':'passed'} for name in native.REQUIRED]
        self.assertTrue(native.events_passed(events))
        self.assertFalse(native.events_passed(events[:-1]))
        self.assertFalse(native.events_passed(events+events[:1]))
        self.assertFalse(native.events_passed([*events[:-1],{'stage':events[-1]['stage'],'status':'skipped'}]))


if __name__=='__main__':unittest.main()
