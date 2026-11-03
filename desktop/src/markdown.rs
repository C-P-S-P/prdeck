//! PR text the way GitHub shows it. GPUI Kit's markdown view does the heavy lifting
//! (selectable text, tables, task lists, HTML like <img width> and <details>, and
//! syntax-highlighted code); this adds images loaded through prdeck with the user's
//! GitHub login, and GitHub's alert boxes (> [!NOTE], > [!WARNING]…).

use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::sync::atomic::{AtomicU64, Ordering};
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
}

impl Images {
    pub fn new() -> (Self, mpsc::UnboundedReceiver<String>) {
        let (tx, rx) = mpsc::unbounded();
        (Images { map: Default::default(), queue: tx, generation: Default::default() }, rx)
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
        ImageFormat::Gif
    } else if b.len() > 12 && &b[..4] == b"RIFF" && &b[8..12] == b"WEBP" {
        ImageFormat::Webp
    } else if b.starts_with(b"BM") {
        ImageFormat::Bmp
    } else if b.starts_with(b"II*\0") || b.starts_with(b"MM\0*") {
        ImageFormat::Tiff
    } else if kind.contains("svg") || head.starts_with("<svg") || head.starts_with("<?xml") {
        ImageFormat::Svg
    } else {
        return None;
    };
    Some(Arc::new(Image::from_bytes(format, bytes)))
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
            if let Ok(v) = u8::from_str_radix(&s[i + 1..i + 3], 16) {
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
    let images = images.clone();
    let id: SharedString = id.into();
    let id = if text.contains("<img") || text.contains("![") {
        SharedString::from(format!("{id}~{}", images.generation.load(Ordering::Relaxed)))
    } else {
        id
    };
    let ext = extensions(&images);
    TextView::markdown(ElementId::Name(id), text.to_string())
        .selectable(true)
        .image_source(move |uri| images.source(uri.as_ref()))
        .markdown_extensions(ext)
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
        .block_renderer("gh-alert", |node, _, _| {
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
                .child(TextView::markdown(ElementId::Name(format!("alert-{:x}", h.finish()).into()), a.body.clone()).selectable(true))
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
