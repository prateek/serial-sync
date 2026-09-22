# Serial Reader experiment

This optional experiment is a modified version of BookOrbit, not an official BookOrbit release. Use vanilla BookOrbit for regular deployment; this patch is retained for separate evaluation. Stock BookOrbit does not gain the navigation or correction behavior described below from the publication adapter.

Base: BookOrbit v3.0.0, commit `6be648b48a9cc376cfeff8953ebf22123583f7eb`. The upstream runtime image is pinned by digest in `Dockerfile`. `reader.patch` is distributed under the upstream AGPL license and additional terms included here. The modified application is named **Serial Reader**, version `3.0.0-serial-reader.1`, with modification date September 21, 2026.

It adds previous/next series controls to the EPUB reader, refreshes available neighbors on focus and every minute, and checks again when clicked. The server applies the user's library and content restrictions to both directions. A book switch requires an acknowledged progress save. Saves from a stale file version are rejected, including replacements not yet scanned. Existing files remain stable as new files arrive.

Explicit replacement of the current file invalidates cached EPUB resources. On reopening a file modified after its saved text position, the reader returns to the saved chapter resource and shows an update notice. Reassembly that changes resource identity or chapter order needs a separately verified migration; this patch does not map arbitrary old CFIs into a differently assembled volume. Reader progress belongs to BookOrbit.

Build with Docker and Git:

```sh
integrations/bookorbit/reader/build.sh /tmp/serial-reader-build serial-reader:3.0.0-1 linux/amd64
```

Use a new build directory each time. The script applies the patch to the exact source revision, builds JavaScript on the local architecture, regenerates application icons from the patched SVG, and overlays that output onto the pinned runtime for the selected architecture. Native dependencies come from that runtime. This patch adds no dependencies or database migrations; its shared type changes are erased at compilation.

The sidebar, sign-in pages, browser titles, installed-app name, and icons use the Serial Reader identity. The existing Legal notices dialog remains accessible and retains linked **Powered by BookOrbit** attribution, copyright, license, and additional terms. It prominently identifies the modified release and its date.

The image serves its corresponding source at `/serial-sync-source.tar.gz`, linked from Legal notices. The archive includes the patched source, lockfile, patched upstream Dockerfile, modification notice, and this integration's build instructions. Rebuilding on a newer upstream release requires rebasing the patch and rerunning the reader acceptance cases. Validate this experiment with disposable state before opting into a deployment; the repository's stock-reader restore procedure does not enable it.
