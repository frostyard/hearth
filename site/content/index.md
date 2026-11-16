---
title: hearth
description: The Protobuf options, plugins and Go runtime that give a first-party app its API, MCP tools, permissions, events and manifest.
hide:
  - navigation
  - toc
---

<div class="fy-hero" markdown>
<div class="fy-hero__text" markdown>
<p class="fy-eyebrow">hearth</p>

# hearth

<p class="fy-hero__lede">hearth is a toolkit for frostyard's first-party apps: Protobuf options, plugins and a Go runtime. 1 Protobuf service gives an app its API, its MCP tools, its permission checks, its events and a versioned manifest, and the 5 cannot drift from each other.</p>

[Install hearth →](getting-started/install.md){ .md-button .md-button--primary }
[Read the guide](guides/options.md){ .md-button }

</div>
<img class="fy-hero__icon" src="assets/logo.svg" alt="">
</div>

[Install hearth](getting-started/install.md) covers the Go toolchain, the 2
commands and how to pin a version.

## What hearth gives you

<div class="grid cards" markdown>

-   **1 service, every surface**

    ---

    Annotate a service with `hearth_rpc`, `hearth_selector` and
    `hearth_event`, and the plugins write the app's manifest from those files.
    The manifest is the app's MCP tools, its permissions and its events, in 1
    JSON file.

-   **A contract that cannot drift**

    ---

    Every RPC declares its permission, its effect and where it is reachable.
    An RPC that declares no permission, no effect or no exposure does not
    build, so a call can never arrive without a rule for it.

-   **A versioned manifest**

    ---

    `manifest.json` is canonical JSON with a JSON Schema. lodge serves an app
    from that file, and an app whose format major lodge does not read is
    disabled with a reason that names both versions.

-   **A compatibility gate**

    ---

    `hearth manifest diff` runs in an app's `make ci`. A removed operation,
    tool or event, or a changed permission, effect, selector or topic, needs
    the app's next major version.

</div>

## Start here

<div class="grid cards" markdown>

- <span class="fy-index">01</span> **[Install](getting-started/install.md)**

    Install the 2 commands, and pin their version.

- <span class="fy-index">02</span> **[The options](guides/options.md)**

    Write the options that give an app its manifest.

- <span class="fy-index">03</span> **[The manifest](guides/manifest.md)**

    Generate a manifest, and check its compatibility against the default branch.

- <span class="fy-index">04</span> **[Manifest format](reference/manifest.md)**

    Look up every member of the format.

</div>
