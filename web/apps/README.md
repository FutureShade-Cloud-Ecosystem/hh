# web/apps

One Vite entry per micro-app, one bundle per micro-app, built from the shared
packages under `web/packages/`. No runtime-composed micro-frontends. Micro-apps
are declared as `frontends:` in `manifest.yaml`, which lane HH-2.2 generates
from the role catalog, so an app directory appears here only once it has a
catalog entry.
