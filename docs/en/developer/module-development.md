# Module development

A module is an independent release and deployment unit. It owns its manifest identity, version, ABI, dependencies, capabilities, configuration declarations, Compose definition, optional hook, templates, and assets.

The frozen deployment must carry everything needed to start. It must not depend on relative paths into a source checkout.

Declare hard dependencies explicitly. Use capability providers for alternatives, ordering edges only for ordering, and resource/provider operations for persistent resources. Scope generated environments to the module, its dependency closure, and explicitly consumed keys. Never log secrets or inject unrelated credentials.
