import hashlib
from pathlib import Path

from authentik.blueprints.models import BlueprintInstance


blueprints = sorted(Path("/blueprints/anas").glob("*.yaml"))
assert blueprints, "no ANAS blueprints are mounted"

instances = {
    instance.path: instance
    for instance in BlueprintInstance.objects.filter(
        path__in=[f"anas/{blueprint.name}" for blueprint in blueprints]
    )
}
for blueprint in blueprints:
    path = f"anas/{blueprint.name}"
    instance = instances.get(path)
    assert instance is not None, f"blueprint instance is missing: {path}"
    assert instance.status == "successful", f"blueprint is not successful: {path}"
    assert instance.last_applied is not None, f"blueprint was never applied: {path}"
    source_hash = hashlib.sha512(blueprint.read_bytes()).hexdigest()
    assert instance.last_applied_hash == source_hash, f"blueprint content is stale: {path}"
