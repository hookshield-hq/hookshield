# Coolify

[`docker-compose.yml`](docker-compose.yml) deploys HookShield on Coolify today: **New resource → Docker Compose Empty**, paste the file, deploy ([docs/SELF_HOSTING.md](../../docs/SELF_HOSTING.md#coolify)). It is also written as a Coolify **service template**, so it can become a one-click service in Coolify's catalog.

## Submitting it to the Coolify catalog

Coolify only accepts one-click services whose repository has **at least 1,000 GitHub stars** ([their contribution guide](https://coolify.io/docs/get-started/contribute/service)). When HookShield qualifies:

1. Fork [coollabsio/coolify](https://github.com/coollabsio/coolify) and branch from `next`.
2. Copy `docker-compose.yml` to `templates/compose/hookshield.yaml`. Keep the metadata comments at the top (`documentation`, `slogan`, `category`, `tags`, `logo`, `port`) and drop the usage notes below them.
3. Add the logo as `svgs/hookshield.svg` (SVG preferred; a square PNG such as `docs/assets/complit-icon.png` is accepted as a last resort — then change the `logo:` line to the PNG name).
4. Test it in a Coolify instance with **Docker Compose Empty** (the same parser as the catalog): the domain, `SERVICE_PASSWORD_POSTGRES` and `SERVICE_REALBASE64_32_ENCRYPTION` must be generated, `/readyz` must turn healthy and sign-up must work over HTTPS.
5. Open the pull request against `next`, and the matching pull request to Coolify's documentation (a service page plus the logo), which their guide requires before the template is merged.

Keep this file in sync with the template after it is merged: changes to the image, variables or healthcheck go to Coolify as a new pull request.
