# OCI-Layout Archive Visualizer

`olav` is the command-line interface for OCI-Layout Archive Visualizer.

It accepts an OCI layout directory or tar archive and opens a split-pane TUI for browsing layout files, previewing text/JSON blobs, inspecting layer tarballs, and exporting selected files.

Top-level JSON is prettified by default. Text previews wrap and show line numbers by default. Layer tarballs are opened asynchronously and show an extraction overlay while they are being indexed.

```sh
go run ./cmd/olav <oci-layout-dir-or-tarball>
```

## Keys

- `Tab` / `Shift+Tab`: switch focus forward/backward between visible panes
- `j` / `k`: move down/up in trees or scroll focused preview
- `Space`: expand/collapse folders in tree panes, or page down in preview panes
- `Enter` / `l` / `Right`: expand or open in tree panes
- `h` / `Left`: collapse in tree panes
- `/`: search focused pane
- `n` / `N`: next/previous preview search match
- `p`: toggle raw/pretty JSON for the focused preview
- `w`: toggle wrapping for the focused preview
- `#`: toggle line numbers for the focused preview
- `z`: toggle zoom for the focused preview
- `e`: export selected file to `./olav-export/`
- `g` / `G`: jump to top/bottom
- `f` / `b`: page down/up in previews
- `Ctrl-D` / `Ctrl-U`: half-page down/up in previews
- `h` / `l` or `Left` / `Right`: horizontal scroll when preview wrapping is disabled
- `0` / `$`: jump to first/last preview column when preview wrapping is disabled
- `?`: show help
- `q`: quit

The bottom line always shows the main key help. Transient messages, search prompts, and export/open results are shown on the line above it.

## Preview Behavior

- JSON can be toggled between raw and pretty views with `p`.
- Pretty JSON is syntax-colored.
- Top-level JSON starts in pretty mode.
- Text files inside layer tarballs start in raw mode.
- Text previews wrap by default and can be toggled with `w`.
- Text previews show line numbers by default and can be toggled with `#`.
- When wrapping is disabled, use horizontal scrolling to inspect long lines.
- Press `z` while focused on a preview pane to zoom it; press `z` again to restore the split-pane layout.
- Python, shell, and YAML files are syntax-highlighted in text previews.
- Zstd-compressed files named `manifest.wall` inside layer tarballs are decompressed in memory and rendered as syntax-colored Chisel manifest JSONL.
- For Chisel manifest JSONL, `p` toggles readable separator spacing while keeping each JSONL item on one line.

## Layer Tarballs

Layer blobs are detected from OCI media types and can be plain tar, gzip-compressed tar, or zstd-compressed tar.

When a layer is opened for the first time, `olav` indexes it in the background and shows a centered overlay:

```text
Extracting tarball.
This can take a while for large tarballs.
```

The selected OCI blob remains highlighted while extraction is running. Once indexed, the layer view shows:

- Layer metadata at the top of the right pane
- Layer file tree in the middle
- Entry details at the bottom
- A third preview pane when the selected layer entry is a text file

Non-text files inside layer tarballs show metadata only.

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
