# Application icons and themes

Color mode and icon artwork are independent. In **My account → Profile → Preferences**, choose system/light/dark, then the site default, Original, Minimal, or an administrator-imported theme. Choices are stored per account and loaded when the same account signs in with another browser. Minimal uses geometric marks, brand colors and consistent spacing.

## Manage themes

ANAS administrators import `.anas-theme.json` files in **Administration → Icon themes**. Import requires the current administrator role and authentication within five minutes; expired authentication is requested inside the page. Imported themes become available to everyone without changing anyone's preference or deploying configuration. Previews use currently authorized applications. Administrators can download built-in and imported packages.

Packages contain data only: local PNG bytes, names, colors and provenance. Scripts, arbitrary CSS, SVG and remote images are rejected. Limits are 32MiB and 128 images per package, square 512–2048px PNG sources of at most 2MiB each, and 4.5:1 text contrast. The private library allows eight imported packages and 32MiB in total. Reimporting identical content creates no duplicate; different content with the same ID is rejected, so use a new theme ID.

Missing application artwork retains its original icon. An unavailable selected theme falls back to the site default with a preferences notice. Packages never define application permissions or URLs. Corrupt files remain preserved in the private data volume for offline investigation and are not selectable.

## Site default and Casdoor

Native Casdoor application icons are global. Personal portal choices never change them. To change the site default, set the source configuration:

```yaml
application_catalog:
  icon_theme: soft
```

For a custom theme, export its package from the portal, place it inside the workspace configuration directory, for example `themes/my-theme.anas-theme.json`, then set:

```yaml
application_catalog:
  icon_theme: custom
  theme_file: themes/my-theme.anas-theme.json
```

Import the source using `anas config import SOURCE -w WORKSPACE`, then review and deploy through the normal plan/apply process. Do not directly edit CLI-managed `workspace/config.yml`. Relative package paths resolve from the workspace configuration directory. Deployment freezes PNG bytes and no longer depends on the source file or network. The portal holds no IAM administrative write credential and cannot execute apply. Preserve configuration-only global packages in the source workspace; the portal cannot export packages absent from its writable library.

## Artwork maintenance and backups

Each built-in module with a web entry keeps its 512px icon, official originals, redraw inputs and `assets/icon-source.json`. Minimal SVG sources live in repository `internal/apptheme/artwork/`. Maintainers run `node scripts/application-themes.mjs --check` to verify provenance, digests and both generated packages. After vector changes, use `--render` to regenerate PNGs with the existing Playwright dependency and Chromium; `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` can select an independent Chrome. Runtime services consume PNG only.

Imported themes and account selections use the portal's existing private data volume and its data backups. Directories use 0700 permissions and theme files 0600. Restart reloads valid packages; sessions are not restored from backups.

## Package fields

| Field | Content |
| --- | --- |
| `schema` | Fixed value `anas.application-theme/v1` |
| `id` | Starts with a lowercase letter, contains lowercase letters/digits/hyphens only, at most 48 characters; custom global packages cannot reserve `original` or `soft` |
| `name` | Display names keyed by supported UI languages, including nonempty `en`; at most 160 bytes per name; unknown language keys are rejected |
| `icons` | Required object mapping stable application names to Base64 PNG bytes; names come from module `applications.name`; omitted artwork retains the original |
| `colors` | Optional `light`/`dark` objects overriding fixed portal color tokens such as `bg`, `surface`, `text` and `primary`, using `#RRGGBB` values only; CSS rules are forbidden |
| `sources` | Optional provenance keyed by the same application names: HTTPS `url`, original `sha256`, and optional `generated`/`note` fields for redraws |

Export a built-in package and assign a new ID to use it as an editing template. `icons` never accepts URLs, file paths or SVG strings. Packages contain no deployment list, business links or permission fields; modules and native configuration remain authoritative for those facts.
