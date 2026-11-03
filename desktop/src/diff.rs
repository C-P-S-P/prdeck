//! Unified-diff patches, split into lines with their old and new line numbers.

#[derive(Clone, Copy, PartialEq, Debug)]
pub enum Kind {
    /// `@@ -18,14 +18,27 @@ class OnlineWriter:`
    Hunk,
    Add,
    Del,
    Context,
    /// `\ No newline at end of file`
    Note,
}

#[derive(Clone, Debug)]
pub struct Line {
    pub kind: Kind,
    pub old: Option<u32>,
    pub new: Option<u32>,
    pub text: String,
}

pub fn parse(patch: &str) -> Vec<Line> {
    let (mut old, mut new) = (0u32, 0u32);
    let mut out = Vec::new();
    for raw in patch.lines() {
        let text = raw.replace('\t', "    ");
        if let Some(rest) = raw.strip_prefix("@@") {
            // "@@ -a,b +c,d @@ heading"
            let mut parts = rest.split_whitespace();
            old = parts.next().and_then(|p| p.trim_start_matches('-').split(',').next()?.parse().ok()).unwrap_or(1);
            new = parts.next().and_then(|p| p.trim_start_matches('+').split(',').next()?.parse().ok()).unwrap_or(1);
            out.push(Line { kind: Kind::Hunk, old: None, new: None, text });
            continue;
        }
        let (kind, body) = match raw.chars().next() {
            Some('+') => (Kind::Add, &text[1..]),
            Some('-') => (Kind::Del, &text[1..]),
            Some('\\') => (Kind::Note, text.as_str()),
            Some(' ') => (Kind::Context, &text[1..]),
            _ => (Kind::Context, text.as_str()),
        };
        let line = match kind {
            Kind::Add => {
                new += 1;
                Line { kind, old: None, new: Some(new - 1), text: body.to_string() }
            }
            Kind::Del => {
                old += 1;
                Line { kind, old: Some(old - 1), new: None, text: body.to_string() }
            }
            Kind::Context => {
                old += 1;
                new += 1;
                Line { kind, old: Some(old - 1), new: Some(new - 1), text: body.to_string() }
            }
            _ => Line { kind, old: None, new: None, text: body.to_string() },
        };
        out.push(line);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn numbers_follow_the_hunks() {
        let lines = parse("@@ -10,3 +10,4 @@ fn x()\n a\n-b\n+c\n+d\n e\n\\ No newline at end of file\n@@ -40 +41 @@\n-z\n+y");
        let got: Vec<(Kind, Option<u32>, Option<u32>, &str)> = lines.iter().map(|l| (l.kind, l.old, l.new, l.text.as_str())).collect();
        assert_eq!(got[0].0, Kind::Hunk);
        assert_eq!(got[1], (Kind::Context, Some(10), Some(10), "a"));
        assert_eq!(got[2], (Kind::Del, Some(11), None, "b"));
        assert_eq!(got[3], (Kind::Add, None, Some(11), "c"));
        assert_eq!(got[4], (Kind::Add, None, Some(12), "d"));
        assert_eq!(got[5], (Kind::Context, Some(12), Some(13), "e"));
        assert_eq!(got[6].0, Kind::Note);
        assert_eq!(got[8], (Kind::Del, Some(40), None, "z"));
        assert_eq!(got[9], (Kind::Add, None, Some(41), "y"));
    }
}
