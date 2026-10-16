# CI image for the core and gui jobs: the Arch base plus the toolchain both
# jobs install, so the jobs' own `pacman -Syu --needed` only tops up. Built and
# pushed by .github/workflows/ci-image.yml to ghcr.io/<owner>/archmaid-ci-arch.
FROM public.ecr.aws/docker/library/archlinux:latest
LABEL org.opencontainers.image.source="https://github.com/Enzord758/archmaid"
LABEL org.opencontainers.image.description="archmaid CI: Arch base with go, gcc, git, make, GTK4 and weston"
RUN pacman -Syu --noconfirm --needed go gcc git make diffutils pkgconf gobject-introspection gtk4 libadwaita adwaita-icon-theme ttf-dejavu weston dbus \
 && pacman -Scc --noconfirm
