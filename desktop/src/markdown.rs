//! PR text the way GitHub shows it. GPUI Kit's markdown view does the heavy lifting
//! (selectable text, tables, task lists, HTML like <img width> and <details>, and
//! syntax-highlighted code); this adds images loaded through prdeck with the user's
//! GitHub login, and GitHub's alert boxes (> [!NOTE], > [!WARNING]…).

use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, OnceLock};

use base64::Engine as _;
use futures::channel::mpsc;
use gpui_kit::component::text::{MarkdownExtensions, MarkdownNode, TextView, markdown_ast};
use gpui_kit::prelude::*;
use gpui_kit::*;

use crate::theme::*;

enum Entry {
    Loading,
    Ready(Arc<Image>),
    Failed,
}

/// Images seen in PR text, by URL. Asking for one that is not here yet queues it for
/// download; the window redraws when it arrives.
#[derive(Clone)]
pub struct Images {
    map: Arc<Mutex<HashMap<String, Entry>>>,
    queue: mpsc::UnboundedSender<String>,
    /// Bumped whenever an image arrives, so views lay themselves out again (table
    /// columns are measured once, and a loading image measures as one pixel).
    generation: Arc<AtomicU64>,
    /// Load GitHub profile photos (off for the sample data's made-up people).
    faces: Arc<AtomicBool>,
}

impl Images {
    pub fn new() -> (Self, mpsc::UnboundedReceiver<String>) {
        let (tx, rx) = mpsc::unbounded();
        (Images { map: Default::default(), queue: tx, generation: Default::default(), faces: Default::default() }, rx)
    }

    /// Stores a downloaded image (or the failure to get one).
    pub fn put(&self, url: String, bytes: Option<(Vec<u8>, String)>) {
        let entry = match bytes.and_then(|(b, kind)| decode(b, &kind)) {
            Some(img) => Entry::Ready(img),
            None => Entry::Failed,
        };
        self.map.lock().unwrap().insert(url, entry);
        self.generation.fetch_add(1, Ordering::Relaxed);
    }

    pub fn show_faces(&self, on: bool) {
        self.faces.store(on, Ordering::Relaxed);
    }

    /// Someone's GitHub profile photo, or their initials until it loads (and when it
    /// cannot, or for the sample data).
    pub fn face(&self, login: &str, size: f32) -> AnyElement {
        let login = login.trim();
        if self.faces.load(Ordering::Relaxed) && !login.is_empty() && login != "ghost" {
            let name = login.trim_end_matches("[bot]");
            if let Some(photo) = self.ready(&format!("https://github.com/{name}.png?size=96")) {
                return img(ImageSource::Image(photo))
                    .size(px(size))
                    .flex_none()
                    .rounded(px(size / 2.))
                    .into_any_element();
            }
        }
        avatar(login, size).into_any_element()
    }

    /// The image when it has arrived; otherwise starts loading it.
    fn ready(&self, url: &str) -> Option<Arc<Image>> {
        let mut map = self.map.lock().unwrap();
        match map.get(url) {
            Some(Entry::Ready(img)) => Some(img.clone()),
            Some(_) => None,
            None => {
                map.insert(url.to_string(), Entry::Loading);
                let _ = self.queue.unbounded_send(url.to_string());
                None
            }
        }
    }

    fn source(&self, url: &str) -> ImageSource {
        let mut map = self.map.lock().unwrap();
        match map.get(url) {
            Some(Entry::Ready(img)) => return ImageSource::Image(img.clone()),
            Some(_) => return blank(),
            None => {}
        }
        if url.starts_with("data:") {
            let entry = data_url(url).map(Entry::Ready).unwrap_or(Entry::Failed);
            let src = match &entry {
                Entry::Ready(img) => ImageSource::Image(img.clone()),
                _ => blank(),
            };
            map.insert(url.to_string(), entry);
            return src;
        }
        map.insert(url.to_string(), Entry::Loading);
        let _ = self.queue.unbounded_send(url.to_string());
        blank()
    }
}

/// A transparent pixel, shown while an image loads.
fn blank() -> ImageSource {
    static BLANK: OnceLock<Arc<Image>> = OnceLock::new();
    let img = BLANK.get_or_init(|| {
        let svg = br#"<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>"#.to_vec();
        Arc::new(Image::from_bytes(ImageFormat::Svg, svg))
    });
    ImageSource::Image(img.clone())
}

/// Picks the image format from the bytes, falling back to the content type.
fn decode(bytes: Vec<u8>, kind: &str) -> Option<Arc<Image>> {
    let b = bytes.as_slice();
    let head = String::from_utf8_lossy(&b[..b.len().min(256)]).trim_start().to_lowercase();
    let format = if b.starts_with(b"\x89PNG") {
        ImageFormat::Png
    } else if b.starts_with(&[0xff, 0xd8]) {
        ImageFormat::Jpeg
    } else if b.starts_with(b"GIF8") {
        // GPUI decodes every frame of a GIF at the full screen size with no memory
        // limit, so a few KB can ask for many GB. Nothing over 16384 px a side can be
        // drawn, and 2^31 pixels (8 GB) still fits long screen recordings.
        let (w, h, frames) = gif_size(b)?;
        if w > 16384 || h > 16384 || w * h * frames > 1 << 31 {
            return None;
        }
        ImageFormat::Gif
    } else if b.len() > 12 && &b[..4] == b"RIFF" && &b[8..12] == b"WEBP" {
        ImageFormat::Webp
    } else if b.starts_with(b"BM") {
        ImageFormat::Bmp
    } else if b.starts_with(b"II*\0") || b.starts_with(b"MM\0*") {
        ImageFormat::Tiff
    } else if kind.contains("svg") || head.starts_with("<svg") || head.starts_with("<?xml") {
        if reads_local_files(b) {
            return None;
        }
        ImageFormat::Svg
    } else {
        return None;
    };
    Some(Arc::new(Image::from_bytes(format, bytes)))
}

/// A GIF's screen width and height and its number of frames, read from its blocks
/// without decoding them. None when the blocks do not end in the trailer (a cut-off
/// file, or a block GPUI does not know): GPUI never stops decoding those, and spins a
/// core for as long as the app runs.
fn gif_size(b: &[u8]) -> Option<(u64, u64, u64)> {
    // Reading past the end gives the trailer (0x3b), so a cut-off file ends the scan.
    let at = |i: usize| b.get(i).map_or(0x3b, |&v| v as usize);
    let table = |flags: usize| if flags & 0x80 != 0 { 3 << ((flags & 7) + 1) } else { 0 };
    // Skips data sub-blocks (a length byte, then the data) and the 0 that ends them,
    // and gives the last one's length too.
    let skip = |mut i: usize| {
        let mut last = 0;
        while i < b.len() && b[i] != 0 {
            last = b[i];
            i += 1 + b[i] as usize;
        }
        (i + 1, last)
    };
    let (w, h) = (at(6) | at(7) << 8, at(8) | at(9) << 8);
    let mut i = 13 + table(at(10));
    let mut frames = 0;
    loop {
        match at(i) {
            // Plain text, graphic control, comment and application extensions.
            0x21 if matches!(at(i + 1), 0x01 | 0xf9 | 0xfe | 0xff) => {
                let (next, last) = skip(i + 2);
                // A graphic control extension must end in 4 bytes. GPUI skips one that
                // does not, then reads on from the wrong place, where it can find
                // frames that are not counted here.
                if at(i + 1) == 0xf9 && last != 4 {
                    break;
                }
                i = next;
            }
            0x2c => {
                frames += 1;
                i = skip(i + 11 + table(at(i + 9))).0;
            }
            _ => break,
        }
    }
    (b.get(i) == Some(&0x3b)).then_some((w as u64, h as u64, frames))
}

/// Whether an SVG has an image whose href is a file on this Mac. GPUI draws SVGs with
/// usvg's default options, which read such files, so PR text could show the user's own
/// files or hang the app on /dev/zero. Parsing with the same usvg finds exactly the
/// hrefs it would follow; web addresses, which usvg never loads, change nothing.
fn reads_local_files(b: &[u8]) -> bool {
    // Every image reference has an href; a gzipped SVG can't be searched, so parse it.
    if !b.starts_with(&[0x1f, 0x8b]) && !b.windows(4).any(|w| w == b"href") {
        return false;
    }
    let hit = AtomicBool::new(false);
    let opts = usvg::Options {
        image_href_resolver: usvg::ImageHrefResolver {
            resolve_data: usvg::ImageHrefResolver::default_data_resolver(),
            resolve_string: Box::new(|href, _| {
                if std::path::Path::new(href).exists() {
                    hit.store(true, Ordering::Relaxed);
                }
                None
            }),
        },
        ..Default::default()
    };
    let _ = usvg::Tree::from_data(b, &opts);
    hit.load(Ordering::Relaxed)
}

/// Decodes `data:image/png;base64,…` and `data:image/svg+xml;utf8,…` URLs.
fn data_url(url: &str) -> Option<Arc<Image>> {
    let (meta, data) = url.strip_prefix("data:")?.split_once(',')?;
    let bytes = if meta.ends_with(";base64") {
        base64::engine::general_purpose::STANDARD.decode(data.trim()).ok()?
    } else {
        percent_decode(data).into_bytes()
    };
    decode(bytes, meta)
}

fn percent_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%' && i + 2 < b.len() {
            // get, not [..]: the two bytes can end inside a multi-byte character.
            if let Some(v) = s.get(i + 1..i + 3).and_then(|h| u8::from_str_radix(h, 16).ok()) {
                out.push(v);
                i += 3;
                continue;
            }
        }
        out.push(b[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// PR text as a markdown view. `id` must be stable for the same text.
pub fn view(id: impl Into<SharedString>, text: &str, images: &Images) -> TextView {
    plain_view(id, text, images).markdown_extensions(extensions(images))
}

/// A markdown view without the GitHub extensions. Every view is made here, so its
/// images load through `images` (left to GPUI, an SVG would be drawn without the check
/// in `decode` and could read local files) and its links go through `open_link`.
fn plain_view(id: impl Into<SharedString>, text: &str, images: &Images) -> TextView {
    let images = images.clone();
    let id: SharedString = id.into();
    let id = if text.contains("<img") || text.contains("![") {
        SharedString::from(format!("{id}~{}", images.generation.load(Ordering::Relaxed)))
    } else {
        id
    };
    TextView::markdown(ElementId::Name(id), text.to_string())
        .selectable(true)
        .image_source(move |uri| images.source(uri.as_ref()))
        .on_link_click(open_link)
}

/// Opens a clicked link in the browser, as the view does by default, but only web and
/// mail links: PR text could otherwise point at file:, smb: or another app's URL
/// scheme, which github.com never lets through.
fn open_link(url: &SharedString, event: &ClickEvent, _: &mut Window, cx: &mut App) {
    let opens = match event {
        ClickEvent::Mouse(click) => matches!(click.up.button, MouseButton::Left | MouseButton::Middle),
        ClickEvent::Keyboard(_) => true,
        ClickEvent::Touch(click) => !click.long_press,
    };
    if opens && web_link(url) {
        cx.open_url(url);
    }
}

fn web_link(url: &str) -> bool {
    let url = url.to_ascii_lowercase();
    ["https://", "http://", "mailto:"].iter().any(|p| url.starts_with(p))
}

/// True when the node or anything inside it is an image.
fn has_image(node: &markdown_ast::Node) -> bool {
    match node {
        markdown_ast::Node::Image(_) | markdown_ast::Node::ImageReference(_) => true,
        markdown_ast::Node::Html(h) => h.value.contains("<img"),
        n => n.children().is_some_and(|c| c.iter().any(has_image)),
    }
}

/// GitHub alerts: a blockquote whose first line is [!NOTE], [!TIP], [!IMPORTANT],
/// [!WARNING] or [!CAUTION] becomes a colored box with a title.
fn extensions(images: &Images) -> MarkdownExtensions {
    let cell_images = images.clone();
    let alert_images = images.clone();
    MarkdownExtensions::default()
        // Tables of screenshots (before | after) get equal columns, like GitHub
        // gives them; the default layout sizes columns by their text alone.
        .block_parser(|node, cx| {
            let markdown_ast::Node::Table(t) = node else { return None };
            if !t.children.iter().any(has_image) {
                return None;
            }
            let rows: Vec<Vec<String>> = t
                .children
                .iter()
                .map(|row| {
                    row.children()
                        .map(|cells| cells.iter().map(|c| cx.node_source(c).unwrap_or("").trim().trim_matches('|').trim().to_string()).collect())
                        .unwrap_or_default()
                })
                .collect();
            let src = cx.node_source(node).unwrap_or("").to_string();
            Some(MarkdownNode::new("gh-image-table", ImageTable { rows }).markdown(src))
        })
        .block_renderer("gh-image-table", move |node, _, _| {
            let Some(t) = node.data::<ImageTable>() else { return div().into_any_element() };
            let cols = t.rows.iter().map(|r| r.len()).max().unwrap_or(1).max(1);
            let mut h = std::collections::hash_map::DefaultHasher::new();
            t.rows.hash(&mut h);
            let base = h.finish();
            div()
                .my(px(8.))
                .flex()
                .flex_col()
                .border_1()
                .border_color(rgb(BORDER_2))
                .children(t.rows.iter().enumerate().map(|(r, row)| {
                    div()
                        .flex()
                        .when(r > 0, |d| d.border_t_1().border_color(rgb(BORDER_2)))
                        .when(r == 0, |d| d.bg(rgb(BG_HEAD)).font_weight(FontWeight::SEMIBOLD))
                        .children((0..cols).map(|c| {
                            let cell = row.get(c).cloned().unwrap_or_default();
                            div()
                                .flex_1()
                                .min_w(px(0.))
                                .px(px(10.))
                                .py(px(8.))
                                .when(c > 0, |d| d.border_l_1().border_color(rgb(BORDER_2)))
                                .child(view(format!("imgtable-{base:x}-{r}-{c}"), &cell, &cell_images))
                        }))
                }))
                .into_any_element()
        })
        .block_parser(|node, cx| {
            let markdown_ast::Node::Blockquote(_) = node else { return None };
            let src = cx.node_source(node)?;
            let mut lines = src.lines();
            let first = lines.next()?.trim().trim_start_matches('>').trim();
            let kind = first.strip_prefix("[!")?.strip_suffix(']')?.to_ascii_uppercase();
            if !["NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION"].contains(&kind.as_str()) {
                return None;
            }
            let inner: Vec<&str> = lines
                .map(|l| {
                    let l = l.trim_start();
                    let l = l.strip_prefix('>').unwrap_or(l);
                    l.strip_prefix(' ').unwrap_or(l)
                })
                .collect();
            Some(MarkdownNode::new("gh-alert", Alert { kind, body: inner.join("\n") }).markdown(src.to_string()))
        })
        .block_renderer("gh-alert", move |node, _, _| {
            let Some(a) = node.data::<Alert>() else { return div().into_any_element() };
            let (title, color, icon_name) = match a.kind.as_str() {
                "TIP" => ("Tip", GREEN, "circle-check"),
                "IMPORTANT" => ("Important", 0xb48ef0, "info"),
                "WARNING" => ("Warning", YELLOW, "triangle-alert"),
                "CAUTION" => ("Caution", RED, "circle-alert"),
                _ => ("Note", BLUE, "info"),
            };
            let mut h = std::collections::hash_map::DefaultHasher::new();
            a.body.hash(&mut h);
            div()
                .my(px(6.))
                .pl(px(14.))
                .py(px(4.))
                .border_l_4()
                .border_color(rgb(color))
                .flex()
                .flex_col()
                .gap(px(6.))
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap(px(8.))
                        .text_color(rgb(color))
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(icon(icon_name, 15., color))
                        .child(title),
                )
                // Through view(), so images in the alert load like the rest of the text.
                .child(view(format!("alert-{:x}", h.finish()), &a.body, &alert_images))
                .into_any_element()
        })
        .parser_revision(1)
}

struct ImageTable {
    rows: Vec<Vec<String>>,
}

struct Alert {
    kind: String,
    body: String,
}

#[cfg(test)]
mod tests {
    // Not `super::*`: GPUI's glob exports its own `test` attribute.
    use super::{data_url, decode, gif_size, percent_decode, reads_local_files, web_link};

    #[test]
    fn only_web_and_mail_links_open() {
        for ok in ["https://github.com/a/b/pull/1", "HTTP://example.com", "mailto:a@example.com"] {
            assert!(web_link(ok), "{ok}");
        }
        for bad in ["file:///System/Applications/Utilities/Terminal.app", "smb://host/share", "vscode://vscode.git/clone?url=x", "javascript:alert(1)", "docs/a.md"] {
            assert!(!web_link(bad), "{bad}");
        }
    }

    /// A GIF on a `w` by `h` screen with `frames` one-pixel frames.
    fn gif(w: u16, h: u16, frames: usize) -> Vec<u8> {
        let mut b = b"GIF89a".to_vec();
        b.extend(w.to_le_bytes());
        b.extend(h.to_le_bytes());
        b.extend([0x80, 0, 0, 0, 0, 0, 0xff, 0xff, 0xff]);
        b.extend(b"\x21\xff\x0bNETSCAPE2.0\x03\x01\x00\x00\x00");
        for _ in 0..frames {
            b.extend([0x21, 0xf9, 4, 0, 0, 0, 0, 0]);
            b.extend([0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0x80, 0, 0, 0, 0xff, 0xff, 0xff, 2, 2, 0x44, 1, 0]);
        }
        b.push(0x3b);
        b
    }

    #[test]
    fn gifs_too_big_to_decode_are_refused() {
        assert_eq!(gif_size(&gif(4000, 4000, 300)), Some((4000, 4000, 300)));
        assert!(decode(gif(1, 1, 1), "image/gif").is_some());
        assert!(decode(gif(1920, 1080, 500), "image/gif").is_some());
        assert!(decode(gif(4000, 4000, 300), "image/gif").is_none());
        assert!(decode(gif(20000, 10, 1), "image/gif").is_none());
    }

    #[test]
    fn gifs_that_hide_frames_from_the_count_are_refused() {
        // After a graphic control extension of 3 bytes, GPUI reads on 2 bytes off and
        // finds 200 frames on a 4000x4000 screen (13 GB) in what is one frame's data
        // when the blocks are read correctly.
        let mut b = gif(4000, 4000, 1);
        b.pop();
        b.extend([0x21, 0xf9, 3, 4, 0, 0, 0]);
        b.extend([0x2c, 0, 0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 5, 2, 2, 0x44, 1, 0]);
        for _ in 0..200 {
            b.extend([0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 32, 0x44, 1]);
            b.extend([0; 31]);
        }
        b.push(0x3b);
        b.extend([1; 59]);
        b.extend([0, 0x3b]);
        assert!(decode(b, "image/gif").is_none());
    }

    #[test]
    fn gifs_gpui_never_finishes_are_refused() {
        let whole = gif(2, 2, 3);
        let n = whole.len();
        let junk = |at: usize, block: &[u8]| [&whole[..at], block, &whole[at..]].concat();
        // The first frame's extension starts 38 bytes in; a comment can go there.
        assert_eq!(&whole[38..40], &[0x21, 0xf9]);
        assert!(decode(junk(38, b"\x21\xfe\x02hi\x00"), "image/gif").is_some());
        // No trailer, cut off in a frame, an unknown block, an unknown extension.
        for bad in [whole[..n - 1].to_vec(), whole[..n - 6].to_vec(), junk(38, b"\x99"), junk(38, b"\x21\x42\x01x\x00")] {
            assert!(gif_size(&bad).is_none());
            assert!(decode(bad, "image/gif").is_none());
        }
    }

    #[test]
    fn percent_decode_keeps_odd_escapes() {
        assert_eq!(percent_decode("%3Csvg%20x"), "<svg x");
        assert_eq!(percent_decode("%zz a%2 é%41"), "%zz a%2 éA");
        // '%' then a character of more than one byte used to panic.
        assert_eq!(percent_decode("%a\u{e9}x"), "%a\u{e9}x");
        assert_eq!(percent_decode("100%\u{2713}"), "100%\u{2713}");
    }

    #[test]
    fn svgs_that_read_local_files_are_refused() {
        let svg = |href: &str| {
            format!(r#"<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"><image href="{href}" width="4" height="4"/></svg>"#)
        };
        let local = concat!(env!("CARGO_MANIFEST_DIR"), "/assets/icons/git-branch.svg");
        assert!(reads_local_files(svg(local).as_bytes()));
        assert!(decode(svg(local).into_bytes(), "image/svg+xml").is_none());
        assert!(data_url(&format!("data:image/svg+xml,{}", svg(local))).is_none());

        for ok in [svg("https://example.com/a.png"), svg("/no/such/file.png"), r#"<svg xmlns="http://www.w3.org/2000/svg"/>"#.into()] {
            assert!(!reads_local_files(ok.as_bytes()), "{ok}");
            assert!(decode(ok.into_bytes(), "image/svg+xml").is_some());
        }
    }

    #[test]
    fn every_markdown_view_loads_its_images_through_prdeck() {
        // A TextView made without image_source loads images itself, and GPUI draws an
        // SVG that reads local files (the body of an alert box did). Only plain_view
        // makes one.
        fn count(dir: &std::path::Path) -> usize {
            let made = [concat!("TextView::", "markdown("), concat!("TextView::", "html(")];
            std::fs::read_dir(dir)
                .unwrap()
                .map(|e| e.unwrap().path())
                .map(|p| if p.is_dir() { count(&p) } else { made.iter().map(|m| std::fs::read_to_string(&p).unwrap_or_default().matches(m).count()).sum() })
                .sum()
        }
        assert_eq!(count(std::path::Path::new(concat!(env!("CARGO_MANIFEST_DIR"), "/src"))), 1);
    }
}
