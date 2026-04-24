---
title: kpil
layout: hextra-home
---

{{< hextra/hero-badge >}}
  <div class="hx-w-2 hx-h-2 hx-rounded-full hx-bg-primary-400"></div>
  <span>v0.0.2 — latest release</span>
  {{< icon name="arrow-circle-right" attributes="height=14" >}}
{{< /hextra/hero-badge >}}

<div class="hx-mt-6 hx-mb-6">
{{< hextra/hero-headline >}}
  Kubernetes + GitHub Copilot,&nbsp;<br class="sm:hx-block hx-hidden" />safely isolated
{{< /hextra/hero-headline >}}
</div>

<div class="hx-mb-12">
{{< hextra/hero-subtitle >}}
  Spin up a read-only kubeconfig and run GitHub Copilot CLI&nbsp;<br class="sm:hx-block hx-hidden" />in an ephemeral container — zero credentials left behind.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx-mb-6">
{{< hextra/hero-button text="Get Started" link="docs/getting-started" >}}
{{< hextra/hero-button text="GitHub" link="https://github.com/qjoly/kpil" style="outline" >}}
</div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Read-only RBAC"
    subtitle="Creates a scoped ServiceAccount with get/list/watch on all resources — secrets never included."
    icon="lock-closed"
  >}}
  {{< hextra/feature-card
    title="Ephemeral by design"
    subtitle="All RBAC resources and the kubeconfig are deleted automatically when you exit."
    icon="trash"
  >}}
  {{< hextra/feature-card
    title="Multi-arch image"
    subtitle="Pre-built for linux/amd64 and linux/arm64. Signed with cosign."
    icon="chip"
  >}}
  {{< hextra/feature-card
    title="Docker & Podman"
    subtitle="Auto-detects the container runtime. Works with both Docker and Podman."
    icon="cube"
  >}}
  {{< hextra/feature-card
    title="Homebrew & Krew"
    subtitle="Install via brew tap or kubectl krew. Pre-built binaries for all major platforms."
    icon="download"
  >}}
  {{< hextra/feature-card
    title="Interactive setup"
    subtitle="Use -i to interactively configure image, network mode, volumes, and entrypoint."
    icon="adjustments"
  >}}
{{< /hextra/feature-grid >}}
