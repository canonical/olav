# olav

`olav` is a terminal OCI image layout visualizer.

It accepts an OCI layout directory or tar archive and opens a split-pane TUI for browsing layout files, previewing text/JSON blobs, inspecting layer tarballs, and exporting selected files.

```sh
go run ./cmd/olav <oci-layout-dir-or-tarball>
```

## Keys

- `Tab`: switch focus between visible panes
- `j` / `k`: move down/up or scroll focused preview
- `Enter` / `l`: expand or open
- `h`: collapse
- `/`: search focused pane
- `n` / `N`: next/previous preview search match
- `p`: toggle raw/pretty JSON for the focused preview
- `e`: export selected file to `./olav-export/`
- `g` / `G`: jump to top/bottom
- `Space` / `f` / `b`: page down/up in previews
- `Ctrl-D` / `Ctrl-U`: half-page down/up in previews
- `?`: show help
- `q`: quit

## Export Layout

Top-level OCI files are exported under:

```text
olav-export/oci-layout/<original OCI path>
```

Files selected inside layer tarballs are exported under:

```text
olav-export/layers/<layer blob path>/<original layer path>
```

Layer file hierarchy is preserved.

## Supported Inputs

- OCI image layout directories
- OCI image layout tar archives
- Layer blobs compressed as plain tar, gzip, or zstd

Docker `docker save` archives are intentionally not supported. Convert them to OCI layout first, for example with `skopeo`.
