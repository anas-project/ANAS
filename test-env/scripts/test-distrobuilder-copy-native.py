#!/usr/bin/env python3
"""Real distrobuilder copy-generator regression, not a product image bake.

Run only inside the explicitly named disposable image-builder VM. A tiny
synthetic rootfs isolates the generator path from slow distribution downloads.
The actual generated ANAS recipe supplies the copy source being tested.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess

spec = importlib.util.spec_from_file_location('runner_image_lab', Path(__file__).with_name('server-incus-runner-image-e2e.py'))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


def main(args):
    lab.require_vm(args.vm_id)
    root, recipe = Path(args.report_root), Path(args.recipe)
    if not root.is_absolute() or root.exists() or not recipe.is_absolute() or recipe.is_symlink() or recipe.stat().st_size > 4 << 20:
        raise RuntimeError('fresh report directory and bounded generated recipe required')
    sources = re.findall(r'- generator: copy\n    source: ([^\n]+)\n    path: /usr/local/bin/forgejo-runner\n', recipe.read_text())
    if sources != ['sources/forgejo-runner']:
        raise RuntimeError('generated recipe does not reference the frozen Runner input')
    os.umask(0o077)
    root.mkdir(mode=0o700)
    (root/'rootfs/etc').mkdir(parents=True)
    (root/'rootfs/etc/os-release').write_text('ID=debian\n')
    (root/'rootfs/etc/passwd').write_text('root:x:0:0:root:/root:/bin/sh\n')
    (root/'rootfs/etc/group').write_text('root:x:0:\n')
    (root/'sources').mkdir()
    payload = b'ANAS frozen copy-generator test input; not a Runner executable\n'
    (root/'sources/forgejo-runner').write_bytes(payload)
    results = []
    for name, source in [('old-path', 'forgejo-runner'), ('fixed-path', sources[0])]:
        candidate = root/(name+'.yml')
        candidate.write_text('image:\n  distribution: debian\n  release: trixie\n  architecture: x86_64\n'
                             'source:\n  downloader: debootstrap\n  url: https://deb.debian.org/debian\n'
                             'packages:\n  manager: apt\nfiles:\n  - generator: copy\n    source: '+source+
                             '\n    path: /usr/local/bin/forgejo-runner\n')
        output = root/name
        output.mkdir()
        result = subprocess.run(['/usr/bin/distrobuilder', 'pack-incus', str(candidate), str(root/'rootfs'), str(output),
                                 '--type=split', '--timeout=30', '--cache-dir='+str(root/(name+'-cache'))],
                                cwd=root, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=40)
        (root/(name+'.log')).write_bytes(result.stdout+result.stderr)
        if name == 'old-path':
            if result.returncode == 0 or b'Failed to stat file' not in result.stderr:
                raise RuntimeError('old copy-path failure was not reproduced')
        else:
            if result.returncode:
                raise RuntimeError('fixed copy path failed with real distrobuilder')
            extracted = subprocess.run(['/usr/bin/unsquashfs', '-cat', str(output/'rootfs.squashfs'),
                                        'usr/local/bin/forgejo-runner'], stdin=subprocess.DEVNULL,
                                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
            if extracted.returncode or extracted.stdout != payload:
                raise RuntimeError('actual packed bytes differ from the frozen source')
        results.append({'case': name, 'exit': result.returncode, 'expected_result': True})
    value = {'native_copy_regression_passed': True, 'product_image': False, 'cases': results,
             'builder_sha256': lab.sha256_file(Path('/usr/bin/distrobuilder'))}
    (root/'result.json').write_text(json.dumps(value))
    print(json.dumps(value))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('vm-id', 'recipe', 'report-root'):
        parser.add_argument('--'+name, required=True)
    main(parser.parse_args())
