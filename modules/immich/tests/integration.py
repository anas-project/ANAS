#!/usr/bin/env python3
"""Disposable real PG + signed OIDC + Immich HTTP fixture; no real IAM/mobile claim."""
import argparse, base64, concurrent.futures, hashlib, json, os, pathlib, secrets, shutil, ssl, struct, subprocess, tempfile, time, urllib.error, urllib.parse, urllib.request, uuid, zlib

parser=argparse.ArgumentParser()
parser.add_argument('--postgres-image',default='anas-immich-test/anas-postgres:18.4.0-r5')
parser.add_argument('--immich-image',default='anas-immich-e2e:3.2.4')
args=parser.parse_args()
repo=pathlib.Path(__file__).resolve().parents[3]
prefix='anas-immich-it-'+uuid.uuid4().hex[:10]
containers=[];volumes=[];network=prefix
root=pathlib.Path(tempfile.mkdtemp(prefix=prefix+'-'))
report={'environment':'local Docker fixture; real PostgreSQL and signed test OIDC; not real IAM/mobile/ANAS workspace restore','checks':{},'observations':{}}
def command(argv,input=None):
    result=subprocess.run(argv,input=input,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    if result.returncode: raise RuntimeError('command failed: '+argv[0]+' '+argv[1]+'\n'+result.stderr[-4000:])
    return result.stdout.strip()
def docker(*argv,input=None): return command(['docker',*argv],input)
def mark(name,value=True): report['checks'][name]=value;print(('PASS: ' if value is True else 'OBSERVED: ')+name,flush=True)
def request(base,path,body=None,token=None,headers=None,raw=False,context=None,method=None):
    headers=dict(headers or {})
    data=body
    if body is not None and isinstance(body,(dict,list)):
        data=json.dumps(body).encode();headers['Content-Type']='application/json'
    if token:headers['Authorization']='Bearer '+token
    req=urllib.request.Request(base+path,data=data,headers=headers,method=method)
    try:
        with urllib.request.urlopen(req,timeout=60,context=context) as response: status=response.status;out=response.read()
    except urllib.error.HTTPError as error:status=error.code;out=error.read()
    if raw:return status,out
    try:return status,json.loads(out) if out else None
    except json.JSONDecodeError:return status,out.decode(errors='replace')
def wait(base,path,context=None,seconds=240):
    until=time.monotonic()+seconds
    while time.monotonic()<until:
        try:
            if request(base,path,context=context)[0]==200:return
        except (OSError,RuntimeError):pass
        time.sleep(2)
    raise RuntimeError('readiness timeout: '+path)
def run(name,image,options,argv=()):
    full=prefix+'-'+name;containers.append(full)
    docker('run','-d','--name',full,'--label','anas.test.immich='+prefix,'--network',network,*options,image,*argv)
    return full
def volume(name):
    name=prefix+'-'+name;docker('volume','create','--label','anas.test.immich='+prefix,name);volumes.append(name);return name
def hostport(container,port):return 'http://'+docker('port',container,str(port)+'/tcp')
def sql(statement):return docker('exec','-i','-e','PGPASSWORD=ProviderTest1',pg,'psql','-U','postgres','-d','immich','-v','ON_ERROR_STOP=1','-At',input=statement)
def login(profile):
    verifier=secrets.token_urlsafe(32);state=secrets.token_urlsafe(24)
    challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip('=')
    status,authorization=request(app,'/api/oauth/authorize',{'redirectUri':'https://photos.example.test/auth/login','state':state,'codeChallenge':challenge})
    assert status in (200,201),(status,authorization)
    status,callback=request(idp,'/fixture/authorize',{'authorizationUrl':authorization['url'],'profile':profile},context=tls)
    assert status==200,(status,callback)
    return request(app,'/api/oauth/callback',{'url':callback['url'],'state':state,'codeVerifier':verifier})
def authenticated(profile):
    status,body=login(profile);assert status in (200,201),(status,body);return body
def upload_response(filename,content,mime):
    boundary='anasfixture'+uuid.uuid4().hex;parts=[]
    for key,value in {'deviceAssetId':filename,'deviceId':'anas-integration','fileCreatedAt':'2026-10-03T00:00:00Z','fileModifiedAt':'2026-10-03T00:00:00Z'}.items():parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="'+key+'"\r\n\r\n'+value+'\r\n').encode())
    parts.append(('--'+boundary+'\r\nContent-Disposition: form-data; name="assetData"; filename="'+filename+'"\r\nContent-Type: '+mime+'\r\n\r\n').encode()+content+b'\r\n');parts.append(('--'+boundary+'--\r\n').encode())
    return request(app,'/api/assets',b''.join(parts),user_token,headers={'Content-Type':'multipart/form-data; boundary='+boundary})
def upload(filename,content,mime):
    status,uploaded=upload_response(filename,content,mime);assert status in (200,201),(status,uploaded)
    asset_id=uploaded['id'];status,download=request(app,'/api/assets/'+asset_id+'/original',token=user_token,raw=True);assert status==200 and hashlib.sha256(download).digest()==hashlib.sha256(content).digest()
    return asset_id
def logout_token(**claims):
    status,signed=request(idp,'/fixture/logout-token',claims,context=tls);assert status==200
    return signed['token']
def backchannel(token):
    return request(app,'/api/oauth/backchannel-logout',urllib.parse.urlencode({'logout_token':token}).encode(),headers={'Content-Type':'application/x-www-form-urlencoded'})
def directory_revocation(sub,event_time,**extra):
    claims={'sub':sub,'sub_id':{'format':'iss_sub','iss':'https://oidc:8443','sub':sub},'events':{'http://schemas.openid.net/event/backchannel-logout':{},'https://schemas.openid.net/secevent/caep/event-type/session-revoked':{'initiating_entity':'policy','event_timestamp':event_time}}}
    claims.update(extra)
    return logout_token(**claims)
def sockets(name,clients):
    config=root/(name+'-config.json');state=root/(name+'-state.json')
    config.write_text(json.dumps({'url':'ws://'+server+':2283/api/socket.io/?EIO=4&transport=websocket','clients':clients}));os.chmod(config,0o600)
    run(name,args.immich_image,['--entrypoint','node','-v',str(root)+':/fixture'],['/fixture/socket-fixture.cjs','/fixture/'+config.name,'/fixture/'+state.name])
    socket_state(state,{key:'connected' for key in clients})
    # Nest's gateway authentication handler runs after the namespace handshake.
    time.sleep(1)
    socket_state(state,{key:'connected' for key in clients})
    return state
def socket_state(state,expected):
    until=time.monotonic()+20
    while time.monotonic()<until:
        try:
            current=json.loads(state.read_text())
            if all(current.get(key)==value for key,value in expected.items()):return
        except (FileNotFoundError,json.JSONDecodeError):pass
        time.sleep(0.2)
    raise AssertionError('server-side websocket credential state did not match '+repr(expected))
def launch_server(name,queue):
    return run(name,args.immich_image,['-p','127.0.0.1::2283','-e','DB_HOSTNAME='+pg,'-e','DB_USERNAME=immich','-e','DB_PASSWORD=ApplicationTest1','-e','DB_DATABASE_NAME=immich','-e','DB_VECTOR_EXTENSION=pgvector','-e','REDIS_HOSTNAME='+queue,'-e','IMMICH_ALLOW_SETUP=false','-e','IMMICH_CONFIG_FILE=/fixture/config.json','-e','NODE_EXTRA_CA_CERTS=/fixture/tls.crt','-v',str(root)+':/fixture:ro','-v',media_volume+':/data'])
try:
    report['images']={name:{'reference':ref,'id':docker('image','inspect','--format','{{.Id}}',ref)} for name,ref in [('postgres',args.postgres_image),('immich',args.immich_image)]}
    command(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(root/'tls.key'),'-out',str(root/'tls.crt'),'-days','1','-subj','/CN=oidc','-addext','subjectAltName=DNS:oidc,DNS:localhost,IP:127.0.0.1'])
    os.chmod(root/'tls.key',0o600)
    shutil.copy2(repo/'modules/immich/tests/oidc-fixture.cjs',root/'oidc-fixture.cjs')
    shutil.copy2(repo/'modules/immich/tests/socket-fixture.cjs',root/'socket-fixture.cjs')
    tls=ssl.create_default_context(cafile=str(root/'tls.crt'))
    config={'oauth':{'enabled':True,'autoRegister':True,'autoLaunch':True,'issuerUrl':'https://oidc:8443','clientId':'immich','clientSecret':'fixture-client-secret','roleClaim':'anas_role','storageLabelClaim':'','scope':'openid profile email'},'passwordLogin':{'enabled':False},'backup':{'database':{'enabled':False}},'machineLearning':{'enabled':False},'job':{'videoConversion':{'concurrency':1}}}
    (root/'config.json').write_text(json.dumps(config));os.chmod(root/'config.json',0o600)
    docker('network','create','--label','anas.test.immich='+prefix,network)
    pg=run('postgres',args.postgres_image,['-e','POSTGRES_USER=postgres','-e','POSTGRES_PASSWORD=ProviderTest1','-v',volume('postgres')+':/var/lib/postgresql'])
    docker('run','--rm','--network',network,'--entrypoint','/bin/sh','-e','PGHOST='+pg,'-e','PGPORT=5432','-e','PGUSER=postgres','-e','PGPASSWORD=ProviderTest1','-e','ANAS_RESOURCE_DATABASE=immich','-e','ANAS_RESOURCE_USERNAME=immich','-e','ANAS_RESOURCE_PASSWORD=ApplicationTest1','-e','ANAS_RESOURCE_POSTGRES_EXTENSIONS=vector,earthdistance','-v',str(repo/'modules/postgres/providers/relational_database')+':/operations:ro',args.postgres_image,'/operations/provision.sh','ensure')
    oidc=run('oidc',args.immich_image,['--network-alias','oidc','-p','127.0.0.1::8443','--entrypoint','node','-v',str(root)+':/fixture:ro'],['/fixture/oidc-fixture.cjs'])
    idp=hostport(oidc,8443).replace('http:','https:');wait(idp,'/.well-known/openid-configuration',tls)
    redis=run('valkey','docker.io/valkey/valkey@sha256:70739f85ad2ee01a726a965584a0f94895f01b0c60b3cc8b0aeef11eaa6888cf',['-v',volume('valkey')+':/data'],['valkey-server','--appendonly','yes'])
    media_volume=volume('media');server=launch_server('server',redis)
    app=hostport(server,2283);wait(app,'/api/server/ping',seconds=360)
    mark('fresh app starts with ordinary PG role and Provider-owned pgvector/earthdistance')
    status,body=login({'sub':'anchor-first-user','email':'first@example.test','role':'user'});assert status==400,(status,body)
    assert sql('SELECT count(*) FROM "user";')=='0';mark('first ordinary OIDC visitor refused without account creation')
    admin=authenticated({'sub':'anchor-admin','email':'admin@example.test','role':'admin'});admin_token=admin['accessToken']
    user=authenticated({'sub':'anchor-user','email':'user@example.test','role':'user'});user_token=user['accessToken']
    assert admin['isAdmin'] is True and user['isAdmin'] is False
    rows=sql('SELECT "oauthId"||\':\'||id FROM "user" ORDER BY "oauthId";');assert 'anchor-admin:' in rows and 'anchor-user:' in rows;mark('native OIDC admin and ordinary accounts created; anchor retained separately from user.id')
    renamed=authenticated({'sub':'anchor-user','email':'renamed@example.test','role':'user'});assert renamed['userId']==user['userId'];mark('same sub with changed email retains internal user.id')
    status,body=login({'sub':'anchor-conflict','email':'user@example.test','role':'user'});assert status==400,(status,body);mark('different sub cannot take an already-bound email')
    for path,body,token in [('/api/auth/admin-sign-up',{'email':'local@example.test','password':'LocalPass1!','name':'Local'},None),('/api/admin/users',{'email':'local@example.test','password':'LocalPass1!','name':'Local'},admin_token),('/api/oauth/unlink',None,user_token),('/api/admin/auth/unlink-all',None,admin_token),('/api/auth/change-password',{'password':'x','newPassword':'LocalPass1!'},user_token)]:
        status,out=request(app,path,body if body is not None else b'',token);assert status in (400,403),(path,status,out)
    mark('HTTP local/setup/password/unlink and admin unlink-all paths denied')
    for path,body,token,method in [('/api/oauth/link',{'url':'https://photos.example.test/auth/login?code=forbidden','state':'forbidden','codeVerifier':'forbidden'},user_token,None),('/api/users/me',{'password':'LocalPass1!'},user_token,'PUT'),('/api/admin/users/'+user['userId'],{'password':'LocalPass1!'},admin_token,'PUT')]:
        status,out=request(app,path,body,token,method=method);assert status in (400,403),(path,status,out)
    mark('HTTP link and user/admin password-update paths denied')
    profiles=[{'sub':'anchor-concurrent','email':'concurrent'+str(i)+'@example.test','role':'user'} for i in range(8)]
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool: concurrent_results=list(pool.map(login,profiles))
    duplicate_count=int(sql('SELECT count(*) FROM "user" WHERE "oauthId"=\'anchor-concurrent\';'))
    report['observations']['concurrent_oauthId_rows']=duplicate_count
    mark('concurrent first-login oauthId rows',duplicate_count)
    assert duplicate_count==1,'DUPLICATE_ANCHOR_CONCURRENT='+str(duplicate_count)
    # Administrator soft-delete path should not allow the same anchor to become a new account.
    deleted=authenticated({'sub':'anchor-softdelete','email':'softdelete@example.test','role':'user'})
    # urllib supports explicit method for the upstream delete route.
    req=urllib.request.Request(app+'/api/admin/users/'+deleted['userId'],data=b'{}',headers={'Authorization':'Bearer '+admin_token,'Content-Type':'application/json'},method='DELETE')
    with urllib.request.urlopen(req,timeout=60) as response:assert response.status in (200,204)
    status,out=login({'sub':'anchor-softdelete','email':'softdelete-new@example.test','role':'user'})
    count=int(sql('SELECT count(*) FROM "user" WHERE "oauthId"=\'anchor-softdelete\';'))
    report['observations']['softdelete_oauthId_rows']=count;mark('softdelete then same-anchor registration rows',count)
    assert count==1 and status>=400,'DUPLICATE_ANCHOR_SOFTDELETE='+str(count)
    # Generate valid image chunks/CRCs and a real H.264 MP4, then run the media jobs.
    def png_chunk(kind,data):return struct.pack('!I',len(data))+kind+data+struct.pack('!I',zlib.crc32(kind+data)&0xffffffff)
    def make_png(color):return b'\x89PNG\r\n\x1a\n'+png_chunk(b'IHDR',struct.pack('!2I5B',16,16,8,2,0,0,0))+png_chunk(b'IDAT',zlib.compress((b'\0'+bytes(color)*16)*16))+png_chunk(b'IEND',b'')
    image=make_png([32,96,160])
    asset_id=upload('fixture.png',image,'image/png');mark('real photo upload and original download hash match')
    docker('run','--rm','--entrypoint','ffmpeg','-v',str(root)+':/fixture',args.immich_image,'-hide_banner','-loglevel','error','-f','lavfi','-i','color=c=blue:s=64x64:r=10','-t','1','-an','-c:v','libx264','-pix_fmt','yuv420p','/fixture/fixture.mp4')
    video=(root/'fixture.mp4').read_bytes();video_id=upload('fixture.mp4',video,'video/mp4');mark('real video upload and original download hash match')
    until=time.monotonic()+90
    while time.monotonic()<until:
        if all(request(app,'/api/assets/'+a+'/thumbnail?size=thumbnail',token=user_token,raw=True)[0]==200 for a in [asset_id,video_id]):break
        time.sleep(2)
    else:raise AssertionError('media thumbnail job did not complete')
    mark('actual photo and video thumbnail jobs complete')
    status,album=request(app,'/api/albums',{'albumName':'Fixture album','assetIds':[asset_id,video_id]},user_token);assert status in (200,201),(status,album)
    album_id=album['id'];status,album=request(app,'/api/albums/'+album_id,token=user_token);assert status==200 and album['assetCount']==2
    assert set(sql('SELECT "assetId" FROM album_asset WHERE "albumId"=\''+album_id+'\';').splitlines())=={asset_id,video_id};mark('real photo/video album management')
    # Keep a real metadata job pending, stop its workers, and restart the dedicated AOF queue.
    status,paused=request(app,'/api/jobs/metadataExtraction',{'command':'pause'},admin_token,method='PUT')
    assert status==200 and paused['queueStatus']['isPaused'] is True,(status,paused)
    queued_image=make_png([96,160,32]);queued_id=upload('queued-fixture.png',queued_image,'image/png')
    status,queues=request(app,'/api/jobs',token=admin_token)
    assert status==200 and queues['metadataExtraction']['jobCounts']['paused']>=1,(status,queues)
    metadata_keys=docker('exec',redis,'valkey-cli','--raw','--scan','--pattern','*metadataExtraction:paused').splitlines();assert len(metadata_keys)==1,metadata_keys
    metadata_key=metadata_keys[0];pending_ids=docker('exec',redis,'valkey-cli','--raw','LRANGE',metadata_key,'0','-1').splitlines()
    assert any(json.loads(docker('exec',redis,'valkey-cli','--raw','HGET',metadata_key.removesuffix(':paused')+':'+job_id,'data'))['id']==queued_id for job_id in pending_ids)
    docker('stop',server);docker('restart',redis)
    until=time.monotonic()+20
    while time.monotonic()<until:
        try:
            if docker('exec',redis,'valkey-cli','--raw','PING')=='PONG':break
        except RuntimeError:pass
        time.sleep(.25)
    else:raise AssertionError('dedicated AOF queue did not restart')
    assert docker('exec',redis,'valkey-cli','--raw','LRANGE',metadata_key,'0','-1').splitlines()==pending_ids,'AOF restart lost the pending job before workers resumed'
    docker('start',server)
    app=hostport(server,2283);wait(app,'/api/server/ping',seconds=180)
    # Native MetadataService bootstrap resumes this queue; verify persisted work before starting it,
    # then completion below, rather than mistaking its already-completed counts for lost jobs.
    status,resumed_queue=request(app,'/api/jobs/metadataExtraction',{'command':'resume'},admin_token,method='PUT');assert status==200,(status,resumed_queue)
    until=time.monotonic()+90
    while time.monotonic()<until:
        if request(app,'/api/assets/'+queued_id+'/thumbnail?size=thumbnail',token=user_token,raw=True)[0]==200:break
        time.sleep(2)
    else:raise AssertionError('AOF-restored metadata job did not finish')
    status,download=request(app,'/api/assets/'+queued_id+'/original',token=user_token,raw=True)
    assert status==200 and hashlib.sha256(download).digest()==hashlib.sha256(queued_image).digest()
    mark('dedicated Valkey AOF restart preserves a real paused metadata job and resumes processing')
    status,key=request(app,'/api/api-keys',{'name':'fixture-key','permissions':['all']},user_token);assert status in (200,201),(status,key)
    api_key=key['secret']
    status,share=request(app,'/api/shared-links',{'type':'INDIVIDUAL','assetIds':[asset_id]},user_token);assert status in (200,201),(status,share)
    share_path='/api/shared-links/me?key='+urllib.parse.quote(share['key']);assert request(app,share_path)[0]==200
    short_profile={'sub':'anchor-short-exp','email':'short-exp@example.test','role':'user'}
    short_session=authenticated(short_profile);short_jti='short-exp-'+uuid.uuid4().hex
    short_token=logout_token(sub=short_profile['sub'],exp=int(time.time())+2,jti=short_jti)
    holder=subprocess.Popen(['docker','exec','-i','-e','PGPASSWORD=ProviderTest1','-e','PGAPPNAME=anas-short-exp-fixture',pg,'psql','-U','postgres','-d','immich','-v','ON_ERROR_STOP=1','-At'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    holder.stdin.write('BEGIN; SELECT id FROM "user" WHERE "oauthId"=\'anchor-short-exp\' FOR UPDATE; SELECT pg_sleep(10); COMMIT;\n');holder.stdin.close()
    lock_deadline=time.monotonic()+5
    while sql("SELECT count(*) FROM pg_stat_activity WHERE application_name='anas-short-exp-fixture' AND wait_event='PgSleep';")!='1':
        assert time.monotonic()<lock_deadline,'short-exp fixture did not acquire its user row lock';time.sleep(.05)
    expired_status=backchannel(short_token)[0]
    assert holder.wait(timeout=15)==0,'short-exp fixture lock holder failed'
    report['observations']['short_exp_after_user_lock_status']=expired_status
    assert expired_status==400,'SHORT_EXP_LOGOUT_ACCEPTED_AFTER_LOCK_WAIT='+str(expired_status)
    assert request(app,'/api/users/me',token=short_session['accessToken'])[0]==200
    assert sql('SELECT count(*) FROM anas_immich_logout_token WHERE jti=\''+short_jti+'\';')=='0'
    mark('signed short-exp logout queued behind a real user lock is rejected without consuming jti or deleting its session')
    socket_clients=sockets('sockets',{'session':{'Authorization':'Bearer '+user_token},'key':{'x-api-key':api_key},'admin':{'Authorization':'Bearer '+admin_token}})
    old_cookie={'Cookie':'immich_access_token='+user_token+'; immich_auth_type=oauth'}
    assert request(app,'/api/users/me',headers=old_cookie)[0]==200
    valid_logout=logout_token(sub='anchor-user',sid='sid-anchor-user');status,out=backchannel(valid_logout);assert status==200,(status,out)
    assert request(app,'/api/users/me',token=user_token)[0]==401 and request(app,'/api/users/me',headers=old_cookie)[0]==401;mark('signed backchannel invalidates prior Bearer and Cookie sessions')
    socket_state(socket_clients,{'session':'disconnected','key':'connected','admin':'connected'});mark('server disconnects old session socket while retaining independent key and other-user sockets')
    status,out=request(app,'/api/users/me',headers={'x-api-key':api_key});report['observations']['api_key_after_backchannel_status']=status;assert status==200;mark('ordinary backchannel retains independent API key')
    status,out=request(app,share_path);report['observations']['share_after_backchannel_status']=status;assert status==200;mark('ordinary backchannel retains independent public share')
    # A signed directory policy event has a distinct, bounded target. Ordinary
    # logout above deliberately retained these credentials; revocation must also
    # work without any live OAuth session or provider access token.
    media_before=sql('SELECT count(*) FROM asset;')
    user_binding=sql('SELECT id||\':\'||"oauthId" FROM "user" WHERE "oauthId"=\'anchor-user\';')
    event_time=float(sql('SELECT extract(epoch from clock_timestamp());'))
    # Credentials created while delivery is pending still inherit the original
    # verified OAuth token time. Their later createdAt must not evade revocation.
    pending_session=authenticated({'sub':'anchor-user','email':'user@example.test','role':'user','issuedAt':int(event_time)-10})
    status,pending_key=request(app,'/api/api-keys',{'name':'created-during-delivery','permissions':['all']},pending_session['accessToken']);assert status in (200,201)
    status,pending_share=request(app,'/api/shared-links',{'type':'INDIVIDUAL','assetIds':[asset_id]},pending_session['accessToken']);assert status in (200,201)
    status,rotating_key=request(app,'/api/api-keys',{'name':'rotated-during-delivery','permissions':['all']},pending_session['accessToken']);assert status in (200,201)
    rotating_sockets=sockets('rotating-key-sockets',{'old':{'x-api-key':rotating_key['secret']},'other':{'Authorization':'Bearer '+admin_token}})
    time.sleep(max(0,int(event_time)+1-time.time())+0.1)
    rotate_session=authenticated({'sub':'anchor-user','email':'user@example.test','role':'user'})
    status,rotated_key=request(app,'/api/api-keys/'+rotating_key['id']+'/rotate',{},rotate_session['accessToken']);assert status in (200,201),(status,rotated_key)
    assert request(app,'/api/users/me',headers={'x-api-key':rotating_key['secret']})[0]==401
    assert request(app,'/api/users/me',headers={'x-api-key':rotated_key['secret']})[0]==200
    socket_state(rotating_sockets,{'old':'disconnected','other':'connected'});mark('same-ID API key rotation disconnects old-secret sockets before returning a fresh secret')
    rotated_socket=sockets('rotated-key-socket',{'fresh':{'x-api-key':rotated_key['secret']}})
    for extra in [{'sid':'sid-anchor-user'},{'sub_id':{'format':'iss_sub','iss':'https://oidc:8443','sub':'anchor-admin'}},{'events':{'http://schemas.openid.net/event/backchannel-logout':{},'https://schemas.openid.net/secevent/caep/event-type/session-revoked':{'initiating_entity':'user','event_timestamp':event_time}}}]:
        assert backchannel(directory_revocation('anchor-user',event_time,**extra))[0]==400
    assert request(app,'/api/users/me',headers={'x-api-key':api_key})[0]==200 and request(app,share_path)[0]==200
    revocation=directory_revocation('anchor-user',event_time)
    assert backchannel(revocation)[0]==200
    assert request(app,'/api/users/me',headers={'x-api-key':rotated_key['secret']})[0]==200
    socket_state(rotated_socket,{'fresh':'connected'});mark('original-cutoff policy preserves newly rotated key and its fresh socket')
    assert request(app,'/api/users/me',headers={'x-api-key':api_key})[0]==401 and request(app,share_path)[0] in (401,403,404)
    assert request(app,'/api/users/me',token=pending_session['accessToken'])[0]==401 and request(app,'/api/users/me',headers={'x-api-key':pending_key['secret']})[0]==401 and request(app,'/api/shared-links/me?key='+urllib.parse.quote(pending_share['key']))[0] in (401,403,404)
    socket_state(socket_clients,{'session':'disconnected','key':'disconnected','admin':'connected'})
    assert sql('SELECT count(*) FROM asset;')==media_before and sql('SELECT id||\':\'||"oauthId" FROM "user" WHERE "oauthId"=\'anchor-user\';')==user_binding
    assert request(app,'/api/users/me',token=admin_token)[0]==200
    mark('signed directory policy event revokes old API key and share without live session; other user, media and binding retained')
    assert login({'sub':'anchor-user','email':'user@example.test','role':'user','issuedAt':int(event_time)-10})[0]>=400
    unknown_sub='anchor-delayed-first-callback'
    assert backchannel(directory_revocation(unknown_sub,event_time))[0]==200
    assert login({'sub':unknown_sub,'email':'delayed-first@example.test','role':'user','issuedAt':int(event_time)-10})[0]>=400
    assert sql('SELECT count(*) FROM "user" WHERE "oauthId"=\'anchor-delayed-first-callback\';')=='0'
    mark('directory cutoff rejects delayed old-token callback, including before first user creation')
    # Fixture admission is restored by issuing a fresh real callback. A durable
    # sender retry uses a fresh token/jti but the same original event timestamp.
    time.sleep(max(0,int(event_time)+1-time.time())+0.1)
    user=authenticated({'sub':'anchor-user','email':'user@example.test','role':'user'});user_token=user['accessToken']
    status,new_key=request(app,'/api/api-keys',{'name':'post-admission-key','permissions':['all']},user_token);assert status in (200,201)
    status,new_share=request(app,'/api/shared-links',{'type':'INDIVIDUAL','assetIds':[asset_id]},user_token);assert status in (200,201)
    new_share_path='/api/shared-links/me?key='+urllib.parse.quote(new_share['key'])
    status,_=login({'sub':'anchor-user','email':'user@example.test','role':'admin','issuedAt':int(event_time)-10});assert status>=400
    persisted_role=sql('SELECT "isAdmin" FROM "user" WHERE "oauthId"=\'anchor-user\';')
    report['observations']['fresh_user_admin_after_delayed_admin_callback']=persisted_role
    assert persisted_role=='f','DELAYED_ADMIN_CALLBACK_CHANGED_FRESH_USER_ROLE='+persisted_role
    assert request(app,'/api/users/me',token=user_token)[1]['isAdmin'] is False and request(app,'/api/users/me',headers={'x-api-key':new_key['secret']})[1]['isAdmin'] is False
    mark('delayed old-admin callback rejects before changing the fresh ordinary user global role or credentials')
    new_socket_clients=sockets('new-sockets',{'session':{'Authorization':'Bearer '+user_token},'key':{'x-api-key':new_key['secret']}})
    assert backchannel(directory_revocation('anchor-user',event_time))[0]==200
    assert request(app,'/api/users/me',token=user_token)[0]==200 and request(app,'/api/users/me',headers={'x-api-key':new_key['secret']})[0]==200 and request(app,new_share_path)[0]==200
    socket_state(new_socket_clients,{'session':'connected','key':'connected'})
    mark('directory retry with fresh jti and original cutoff preserves subsequently issued session, key and share')
    active_event_time=float(sql('SELECT extract(epoch from clock_timestamp());'))
    active_revocation=directory_revocation('anchor-user',active_event_time)
    assert backchannel(active_revocation)[0]==200
    assert request(app,'/api/users/me',token=user_token)[0]==401 and request(app,'/api/users/me',headers={'x-api-key':new_key['secret']})[0]==401 and request(app,new_share_path)[0] in (401,403,404)
    socket_state(new_socket_clients,{'session':'disconnected','key':'disconnected'})
    assert request(app,'/api/users/me',token=admin_token)[0]==200
    mark('directory policy event revokes active session and key sockets on the server without client cooperation')
    time.sleep(max(0,int(active_event_time)+1-time.time())+0.1)
    user=authenticated({'sub':'anchor-user','email':'user@example.test','role':'user'});user_token=user['accessToken']
    replay_status=backchannel(valid_logout)[0];report['observations']['backchannel_replay_status']=replay_status;assert replay_status==400;mark('repeated signed logout token rejected by durable jti guard')
    replay_profile={'sub':'anchor-replay','email':'replay@example.test','role':'user'}
    initial_replay=authenticated(replay_profile);sidless_logout=logout_token(sub='anchor-replay')
    assert backchannel(sidless_logout)[0]==200 and request(app,'/api/users/me',token=initial_replay['accessToken'])[0]==401
    new_replay=authenticated(replay_profile);replay_status=backchannel(sidless_logout)[0]
    new_session_status=request(app,'/api/users/me',token=new_replay['accessToken'])[0]
    report['observations']['sidless_replay_response_status']=replay_status;report['observations']['new_session_after_sidless_replay_status']=new_session_status
    assert replay_status==400 and new_session_status==200,'SIDLESS_REPLAY_REVOKED_NEW_SESSION='+str(new_session_status)
    mark('sid-less logout replay leaves subsequently created session valid')
    concurrent_profile={'sub':'anchor-logout-concurrent','email':'logout-concurrent@example.test','role':'user'}
    concurrent_session=authenticated(concurrent_profile);concurrent_logout=logout_token(sub=concurrent_profile['sub'])
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:logout_results=list(pool.map(backchannel,[concurrent_logout]*8))
    logout_statuses=[r[0] for r in logout_results];assert logout_statuses.count(200)==1 and logout_statuses.count(400)==7,logout_statuses
    assert request(app,'/api/users/me',token=concurrent_session['accessToken'])[0]==401
    report['observations']['concurrent_logout_statuses']=logout_statuses;mark('eight concurrent logout requests consume one jti exactly once')
    invalid=[logout_token(sub='anchor-admin',aud='wrong-client'),logout_token(sub='anchor-admin',iss='https://wrong.example.test'),logout_token(sub='anchor-admin',nonce='invalid'),logout_token(sub='anchor-admin',nonce=''),logout_token(sub='anchor-admin',nonce=None),logout_token(sub='anchor-admin',events={'http://schemas.openid.net/event/backchannel-logout':'wrong-type'}),logout_token(sub='anchor-admin',events={'http://schemas.openid.net/event/backchannel-logout':[]}),logout_token(sub='anchor-admin',iat=int(time.time())-3600)]
    invalid.append(valid_logout[:-3]+'bad')
    invalid.append(logout_token(sub='anchor-admin',jti=''))
    for bad_token in invalid:assert backchannel(bad_token)[0]==400
    assert request(app,'/api/users/me',token=admin_token)[0]==200;mark('wrong audience/issuer/signature/nonce presence/event shape/age logout rejected without invalidating admin session')
    regular=authenticated({'sub':'anchor-logout','email':'logout@example.test','role':'user'});cookie={'Cookie':'immich_access_token='+regular['accessToken']+'; immich_auth_type=oauth'}
    status,child=request(app,'/api/sessions',{'duration':600,'deviceType':'ANAS logout fixture'},token=regular['accessToken']);assert status==201,(status,child)
    status,grandchild=request(app,'/api/sessions',{'duration':600},token=child['token']);assert status==201,(status,grandchild)
    logout_sockets=sockets('ordinary-logout-sockets',{'parent':{'Authorization':'Bearer '+regular['accessToken']},'child':{'Authorization':'Bearer '+child['token']},'grandchild':{'Authorization':'Bearer '+grandchild['token']},'other':{'Authorization':'Bearer '+admin_token}})
    status,out=request(app,'/api/auth/logout',{},headers=cookie);assert status==200 and out['successful'] is True and out['redirectUri'].startswith('https://oidc:8443/logout');assert request(app,'/api/users/me',headers=cookie)[0]==401;mark('ordinary OIDC logout invalidates old Cookie and returns IAM end-session URL')
    assert request(app,'/api/users/me',token=child['token'])[0]==401 and request(app,'/api/users/me',token=grandchild['token'])[0]==401
    socket_state(logout_sockets,{'parent':'disconnected','child':'disconnected','grandchild':'disconnected','other':'connected'});mark('ordinary logout disconnects parent and recursively delegated session sockets while retaining another user')
    docker('restart',server);app=hostport(server,2283);wait(app,'/api/server/ping',seconds=180)
    assert backchannel(sidless_logout)[0]==400 and request(app,'/api/users/me',token=new_replay['accessToken'])[0]==200;mark('jti replay guard survives server restart and preserves new session')
    resumed=authenticated({'sub':'anchor-user','email':'user@example.test','role':'user'});user_token=resumed['accessToken'];assert resumed['userId']==user['userId']
    assert sql('SELECT count(*) FROM "user" WHERE "oauthId"=\'anchor-admin\';')=='1'
    for a,content in [(asset_id,image),(video_id,video)]:
        status,download=request(app,'/api/assets/'+a+'/original',token=user_token,raw=True);assert status==200 and hashlib.sha256(download).digest()==hashlib.sha256(content).digest()
    status,album=request(app,'/api/albums/'+album_id,token=user_token);assert status==200 and album['assetCount']==2
    assert set(sql('SELECT "assetId" FROM album_asset WHERE "albumId"=\''+album_id+'\';').splitlines())=={asset_id,video_id};mark('restart retains identity, photo/video hashes and album')
    # A second bounded queue uses only a fresh container tmpfs. The normal AOF volume and host disk remain intact.
    docker('stop',server)
    full_redis=run('full-valkey','docker.io/valkey/valkey@sha256:70739f85ad2ee01a726a965584a0f94895f01b0c60b3cc8b0aeef11eaa6888cf',['--tmpfs','/data:rw,size=4m'],['valkey-server','--appendonly','yes','--appendfsync','everysec'])
    failed_server=launch_server('full-queue-server',full_redis);app=hostport(failed_server,2283);wait(app,'/api/server/ping',seconds=180)
    for index in range(12):
        reply=docker('exec','-i',full_redis,'valkey-cli','--raw','-x','SET','anas:test:filler:'+str(index),input='x'*(1024*1024))
        time.sleep(.25)
        if reply!='OK':break
    until=time.monotonic()+20
    while time.monotonic()<until:
        if 'aof_last_write_status:err' in docker('exec',full_redis,'valkey-cli','--raw','INFO','persistence'):break
        time.sleep(.25)
    else:raise AssertionError('bounded queue did not produce real AOF ENOSPC')
    failed_image=make_png([160,32,96]);before_assets=int(sql('SELECT count(*) FROM asset;'))
    status,failed_upload=upload_response('full-queue-fixture.png',failed_image,'image/png')
    report['observations']['upload_on_full_queue_status']=status
    assert status>=500,(status,failed_upload)
    mark('real AOF ENOSPC prevents successful photo-upload response')
    after_assets=int(sql('SELECT count(*) FROM asset;'));report['observations']['full_queue_asset_delta']=after_assets-before_assets
    assert after_assets==before_assets,'QUEUE_FAILURE_LEFT_ASSET_ROW='+str(after_assets-before_assets)
    docker('stop',failed_server);docker('start',server);app=hostport(server,2283);wait(app,'/api/server/ping',seconds=180)
    recovered_id=upload('full-queue-fixture.png',failed_image,'image/png')
    until=time.monotonic()+90
    while time.monotonic()<until:
        if request(app,'/api/assets/'+recovered_id+'/thumbnail?size=thumbnail',token=user_token,raw=True)[0]==200:break
        time.sleep(2)
    else:raise AssertionError('retry after queue recovery did not process metadata')
    mark('queue-write failure rolls back the new asset and a healthy retry processes the photo')
except BaseException as error:
    report['failure']=str(error);print('FAIL: '+str(error),flush=True)
    for container in containers:
        try:
            logfile=root/(container+'.log');logfile.write_text(docker('logs','--tail','120',container));os.chmod(logfile,0o600)
        except BaseException:pass
    raise
finally:
    (root/'report.json').write_text(json.dumps(report,indent=2));os.chmod(root/'report.json',0o600)
    print('REPORT: '+str(root/'report.json'),flush=True)
    for container in reversed(containers):
        try:docker('rm','-f','-v',container)
        except BaseException:pass
    for name in volumes:
        try:docker('volume','rm',name)
        except BaseException:pass
    try:docker('network','rm',network)
    except BaseException:pass
