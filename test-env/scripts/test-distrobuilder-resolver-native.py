#!/usr/bin/env python3
"""Isolate build-chroot resolver failure and guest tmpfiles setup with real tools.

This tiny synthetic image is only a regression control, never a Runner image.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

spec=importlib.util.spec_from_file_location('runner_image_lab',Path(__file__).with_name('server-incus-runner-image-e2e.py'))
lab=importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)
RULE='L+ /etc/resolv.conf - - - - /run/systemd/resolve/resolv.conf'
OLD='ln -sf /run/systemd/resolve/resolv.conf /etc/resolv.conf'


def main(args):
    lab.require_vm(args.vm_id)
    root,recipe=Path(args.report_root),Path(args.recipe)
    if not root.is_absolute() or root.exists() or not recipe.is_absolute() or recipe.is_symlink() or recipe.stat().st_size>4<<20:
        raise RuntimeError('fresh fixture directory and bounded generated recipe required')
    text=recipe.read_text()
    if RULE not in text or OLD in text or 'path: /usr/lib/tmpfiles.d/anas-resolver.conf' not in text:
        raise RuntimeError('generated recipe lacks the guest-boot resolver fix')
    os.umask(0o077)
    root.mkdir(mode=0o700)
    results=[]
    for name,action in [('old-hook',OLD),('guest-boot','true')]:
        case=root/name;case.mkdir();fs=case/'rootfs';fs.mkdir()
        for directory in ('etc','proc','sys','dev','run','tmp','root'):(fs/directory).mkdir()
        (fs/'etc/os-release').write_text('ID=debian\n')
        (fs/'etc/passwd').write_text('root:x:0:0:root:/root:/bin/sh\n')
        (fs/'etc/group').write_text('root:x:0:\n')
        for binary,destination in [('/usr/bin/dash','bin/sh'),('/usr/bin/ln','bin/ln')]:
            target=fs/destination;target.parent.mkdir(parents=True,exist_ok=True)
            shutil.copyfile(binary,target);target.chmod(0o755)
            listing=subprocess.run(['/usr/bin/ldd',binary],capture_output=True,check=True,timeout=10).stdout.decode()
            for library in re.findall(r'(/[\w/+.\-]+)',listing):
                source=Path(library)
                if source.is_file():
                    target=fs/library.lstrip('/');target.parent.mkdir(parents=True,exist_ok=True)
                    shutil.copyfile(source,target);target.chmod(0o755)
        candidate=case/'recipe.yml'
        config=('image:\n  distribution: debian\n  release: trixie\n  architecture: x86_64\n'
                'source:\n  downloader: debootstrap\n  url: https://deb.debian.org/debian\npackages:\n  manager: apt\n')
        if name=='guest-boot':
            config+='files:\n  - generator: dump\n    path: /usr/lib/tmpfiles.d/anas-resolver.conf\n    content: |-\n      '+RULE+'\n'
        config+='actions:\n  - trigger: post-files\n    action: |-\n      #!/bin/sh\n      set -eu\n      '+action+'\n'
        candidate.write_text(config)
        output=case/'output';output.mkdir()
        result=subprocess.run(['/usr/bin/distrobuilder','pack-incus',str(candidate),str(fs),str(output),
            '--type=split','--timeout=30','--cache-dir='+str(case/'cache')],cwd=case,stdin=subprocess.DEVNULL,capture_output=True,timeout=40)
        # Controlled synthetic recipe only; contains no credentials or user data.
        (case/'tool.log').write_bytes(result.stdout+result.stderr)
        if name=='old-hook':
            if result.returncode==0 or b'Failed to run post-files' not in result.stderr:raise RuntimeError('old resolver hook failure not reproduced')
        else:
            if result.returncode:raise RuntimeError('fixed declarative resolver packing failed')
            unpacked=case/'unpacked'
            subprocess.run(['/usr/bin/unsquashfs','-no-progress','-d',str(unpacked),str(output/'rootfs.squashfs')],
                           stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,check=True,timeout=15)
            if (unpacked/'usr/lib/tmpfiles.d/anas-resolver.conf').read_text().strip()!=RULE:raise RuntimeError('packed resolver rule does not match generated input')
            subprocess.run(['/usr/bin/systemd-tmpfiles','--root='+str(unpacked),'--create','--prefix=/etc/resolv.conf','anas-resolver.conf'],
                           stdin=subprocess.DEVNULL,capture_output=True,check=True,timeout=15)
            if os.readlink(unpacked/'etc/resolv.conf')!='/run/systemd/resolve/resolv.conf':raise RuntimeError('guest tmpfiles did not install resolver link')
        results.append({'case':name,'exit':result.returncode})
    summary={'native_resolver_regression_passed':True,'product_image':False,'cases':results,'builder_sha256':lab.sha256_file(Path('/usr/bin/distrobuilder'))}
    (root/'result.json').write_text(json.dumps(summary));print(json.dumps(summary))


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id','recipe','report-root'):parser.add_argument('--'+name,required=True)
    main(parser.parse_args())
