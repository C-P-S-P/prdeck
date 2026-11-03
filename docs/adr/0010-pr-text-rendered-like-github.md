# 0010. PR text on the desktop is rendered like github.com

- Status: accepted
- Date: 2026-11-03

## Context

On the desktop there is room for what a terminal cannot show: screenshots at full size, before/after tables of images, real checkboxes, colored alerts, highlighted code. PR text should look as it does on github.com.

## Decision

- GPUI Kit's text view renders the markdown, including HTML such as `<img width>`, `<details>` and `<br>`, keeps text selectable, and highlights code with syntect.
- The core only lightly cleans text for the desktop (`GitHubForDesktop`): drops HTML comments, links `@mentions` to profiles, turns `:rocket:` into 🚀, and labels suggestion blocks. HTML and alerts are left for the renderer.
- Two plugins add what the view lacks: `> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]` and `[!CAUTION]` become colored boxes with a title, and tables that contain images get equal columns, so before/after screenshots sit side by side.
- Images are downloaded by the core with the `image` request ([0002](0002-github-through-the-gh-login.md)), several at a time, and cached by URL; `data:` URLs are decoded in place. A view lays itself out again when an image arrives, so table columns are measured with the real image.
- Links and commits open in the browser; a commit's hash copies on click.

## Consequences

- Private repo screenshots and attachments show, because they are fetched with the gh login.
- Images from hosts that refuse unknown clients may not load; the alt text and layout stay.
- Rendering depends on GPUI Kit's markdown parser; anything it cannot parse shows as text.

## Alternatives considered

- **Render GitHub's `bodyHTML` in a web view.** Exact, but a browser engine inside the app ([0008](0008-desktop-app-in-rust-with-gpui.md)).
- **Our own GPUI markdown renderer.** Full control, but no text selection without writing that too.
