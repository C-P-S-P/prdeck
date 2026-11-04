//! Colors and small building blocks from the PR Deck design: dark, boxy, square corners.

use gpui_kit::component::{Theme, ThemeMode};
use gpui_kit::*;

pub const BG: u32 = 0x1b1d22;
pub const BG_DEEP: u32 = 0x16181c;
pub const BG_SIDE: u32 = 0x1e2025;
pub const BG_HEAD: u32 = 0x22252b;
pub const BG_INPUT: u32 = 0x1f2227;
pub const BG_CODE: u32 = 0x2a2e35;
pub const BG_HOVER: u32 = 0x24272d;
pub const BG_SEL: u32 = 0x232c3a;
pub const BG_ACTIVE: u32 = 0x24324a;
pub const BG_BADGE: u32 = 0x26292f;

pub const BORDER: u32 = 0x2c3038;
pub const BORDER_2: u32 = 0x33373f;
pub const BORDER_3: u32 = 0x3a3f48;
pub const BORDER_POP: u32 = 0x434852;
pub const RING_ACTIVE: u32 = 0x4b80c8;

pub const TEXT: u32 = 0xe4e6ea;
pub const TEXT_BRIGHT: u32 = 0xeef0f3;
pub const TEXT_BODY: u32 = 0xd5d8dd;
pub const TEXT_SOFT: u32 = 0xc9cdd4;
pub const SOFT: u32 = 0x9aa1ad;
pub const DIM: u32 = 0x8a919d;

pub const BLUE: u32 = 0x6fa8f5;
pub const BLUE_LIGHT: u32 = 0x9cc4fa;
pub const GREEN: u32 = 0x8cc47a;
pub const RED: u32 = 0xe57b7b;
pub const YELLOW: u32 = 0xe0c070;
pub const PURPLE: u32 = 0xa371f7;
pub const PRIMARY: u32 = 0x3b6fb6;

pub const MONO: &str = "Menlo";

/// Dark theme for the gpui-component widgets (inputs, markdown) in the design's colors.
pub fn apply(window: &mut Window, cx: &mut App) {
    Theme::change(ThemeMode::Dark, Some(window), cx);
    let t = Theme::global_mut(cx);
    t.font_size = px(13.);
    t.mono_font_family = MONO.into();
    t.mono_font_size = px(12.5);
    t.radius = px(0.);
    t.radius_lg = px(0.);
    t.shadow = false;
    t.background = rgb(BG).into();
    t.foreground = rgb(TEXT_BODY).into();
    t.border = rgb(BORDER_2).into();
    t.input = rgb(BORDER_3).into();
    t.ring = rgb(RING_ACTIVE).into();
    t.muted = rgb(BG_CODE).into();
    t.muted_foreground = rgb(DIM).into();
    t.accent = rgb(BG_HOVER).into();
    t.accent_foreground = rgb(TEXT).into();
    t.link = rgb(BLUE).into();
    t.link_hover = rgb(BLUE_LIGHT).into();
    t.link_active = rgb(BLUE_LIGHT).into();
    t.selection = rgba(0x3b6fb666).into();
    t.caret = rgb(BLUE).into();
    t.popover = rgb(BG_HEAD).into();
    t.popover_foreground = rgb(TEXT).into();
    t.table = rgb(BG_SIDE).into();
    t.table_head = rgb(BG_HEAD).into();
    t.table_even = rgb(BG_SIDE).into();
    t.table_row_border = rgb(BORDER).into();
    t.scrollbar = rgba(0x00000000).into();
    t.scrollbar_thumb = rgb(BORDER_3).into();
    t.scrollbar_thumb_hover = rgb(BORDER_POP).into();
}

/// A small key label, like ⌘K.
pub fn kbd(text: impl Into<SharedString>) -> Div {
    div()
        .px(px(4.))
        .border_1()
        .border_color(rgb(BORDER_3))
        .bg(rgb(BG_INPUT))
        .text_color(rgb(TEXT_SOFT))
        .text_size(px(10.5))
        .font_family(MONO)
        .child(text.into())
}

/// Two-letter avatar in a color picked from the login.
pub fn avatar(login: &str, size: f32) -> Div {
    const PAIRS: [(u32, u32); 7] = [
        (0x24402d, 0xa6d896),
        (0x233550, 0x9cc4fa),
        (0x45361c, 0xecd08a),
        (0x3a2a4d, 0xd2b4f2),
        (0x4a2629, 0xf2a7a7),
        (0x1f4042, 0x8ad6d6),
        (0x2c3038, 0xc3c8d0),
    ];
    let h = login.bytes().fold(7u32, |h, b| h.wrapping_mul(31).wrapping_add(b as u32));
    let (bg, fg) = PAIRS[(h as usize) % PAIRS.len()];
    let ini: String = login.chars().filter(|c| c.is_alphanumeric()).take(2).collect::<String>().to_uppercase();
    div()
        .flex_none()
        .size(px(size))
        .flex()
        .items_center()
        .justify_center()
        .bg(rgb(bg))
        .text_color(rgb(fg))
        .text_size(px(size * 0.45))
        .font_weight(FontWeight::SEMIBOLD)
        .child(ini)
}

/// An icon from desktop/assets/icons or the bundled set, tinted.
pub fn icon(name: &str, size: f32, color: u32) -> Svg {
    svg().path(SharedString::from(format!("icons/{name}.svg"))).size(px(size)).flex_none().text_color(rgb(color))
}

/// Section label: small, uppercase, dim.
pub fn caption(text: impl Into<SharedString>) -> Div {
    div().text_size(px(11.)).font_weight(FontWeight::SEMIBOLD).text_color(rgb(DIM)).child(text.into().to_uppercase())
}

/// A bordered button look (the caller adds id and click handler).
pub fn button_look(id: impl Into<ElementId>) -> Stateful<Div> {
    div()
        .id(id)
        .h(px(30.))
        .px(px(10.))
        .flex()
        .items_center()
        .gap(px(6.))
        .border_1()
        .border_color(rgb(BORDER_3))
        .text_color(rgb(TEXT))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(BG_HOVER)))
}

pub fn primary_look(id: impl Into<ElementId>) -> Stateful<Div> {
    div()
        .id(id)
        .h(px(30.))
        .px(px(12.))
        .flex()
        .items_center()
        .gap(px(6.))
        .bg(rgb(PRIMARY))
        .border_1()
        .border_color(rgb(RING_ACTIVE))
        .text_color(rgb(0xffffff))
        .cursor_pointer()
        .hover(|s| s.bg(rgb(0x4579c2)))
}
