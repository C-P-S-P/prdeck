package demo

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/C-P-S-P/prdeck/internal/github"
)

// Sample diffs: hand-written for the push-source PR (#4821), made up for the rest.

const pushSourcePy = `@@ -0,0 +1,30 @@
+"""Push sources accept rows written by clients and land them in the online store."""
+
+from dataclasses import dataclass, field
+from datetime import timedelta
+
+from registry.online_writer import OnlineWriter
+from registry.schema import FeatureViewSchema, SchemaError
+
+
+@dataclass
+class StreamPushSource:
+    name: str
+    schema: FeatureViewSchema
+    batch: timedelta = timedelta(milliseconds=200)
+    _writer: OnlineWriter = field(default=None, repr=False)
+
+    def push(self, name, df, *, allow_registry_cache=True):
+        """Validate df against the feature view and queue it for the online store."""
+        fv = self._get_feature_view(name, allow_registry_cache)
+        self._validate(fv, df)
+        self._writer.enqueue(fv, df, flush_after=self.batch)
+        return len(df)
+
+    def _validate(self, fv, df):
+        missing = [c for c in fv.columns if c not in df.columns]
+        if missing:
+            raise SchemaError(f"{fv.name}: missing columns {missing}")
+        extra = [c for c in df.columns if c not in fv.columns]
+        if extra:
+            raise SchemaError(f"{fv.name}: unknown columns {extra}")`

const pushSourceProto = `@@ -0,0 +1,13 @@
+syntax = "proto3";
+
+package registry;
+
+import "google/protobuf/duration.proto";
+
+message StreamPushSource {
+  string name = 1;
+  string feature_view = 2;
+  google.protobuf.Duration batch = 3;
+  // Rows that fail schema validation are dropped and counted here.
+  string dead_letter_topic = 4;
+}`

const onlineWriterPy = `@@ -18,14 +18,27 @@ class OnlineWriter:
     def __init__(self, store, *, max_batch=500):
         self._store = store
         self._max_batch = max_batch
-        self._pending = []
+        self._pending = {}
+        self._timer = None
 
-    def write(self, fv, rows):
-        self._store.write(fv, rows)
+    def enqueue(self, fv, df, *, flush_after):
+        """Batch rows per feature view; flush when full or after flush_after."""
+        self._pending.setdefault(fv.name, []).extend(df.to_dict("records"))
+        if len(self._pending[fv.name]) >= self._max_batch:
+            self.flush(fv.name)
+        elif self._timer is None:
+            self._timer = self._schedule(flush_after, self.flush_all)
+
+    def flush(self, name):
+        rows = self._pending.pop(name, [])
+        if rows:
+            self._store.write(name, rows)
 
-    def flush(self):
-        pass
+    def flush_all(self):
+        self._timer = None
+        for name in list(self._pending):
+            self.flush(name)`

const registryCacheDefault = `@@ -14,7 +14,7 @@ class StreamPushSource:
     batch: timedelta = timedelta(milliseconds=200)
     _writer: OnlineWriter = field(default=None, repr=False)
 
-    def push(self, name, df, *, allow_registry_cache=True):
+    def push(self, name, df, *, allow_registry_cache=False):
         """Validate df against the feature view and queue it for the online store."""
         fv = self._get_feature_view(name, allow_registry_cache)
         self._validate(fv, df)`

// samplePatch makes up a patch with the given numbers of added and removed lines.
func samplePatch(file string, add, del int) string {
	add, del = min(add, 60), min(del, 30)
	stem := strings.TrimSuffix(path.Base(file), path.Ext(file))
	line := func(i int, new bool) string {
		v := "old"
		if new {
			v = "new"
		}
		switch path.Ext(file) {
		case ".go":
			return fmt.Sprintf("\t%s%d := %s.Load(ctx, %q)", v, i, stem, v)
		case ".proto":
			return fmt.Sprintf("  string %s_%d = %d;", v, i, i+1)
		default:
			return fmt.Sprintf("    %s_%d = %s.load(%d)", v, i, stem, i)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@@ -12,%d +12,%d @@\n", del+3, add+3)
	b.WriteString(" " + line(0, false) + "\n")
	for i := 1; i <= del; i++ {
		b.WriteString("-" + line(i, false) + "\n")
	}
	for i := 1; i <= add; i++ {
		b.WriteString("+" + line(i, true) + "\n")
	}
	b.WriteString(" " + line(99, false) + "\n " + line(100, false))
	return b.String()
}

func counts(patch string) (add, del int) {
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "+"):
			add++
		case strings.HasPrefix(l, "-"):
			del++
		}
	}
	return
}

func fileDiff(p, status, patch string) github.FileDiff {
	a, d := counts(patch)
	return github.FileDiff{Path: p, Status: status, Additions: a, Deletions: d, Patch: patch}
}

func (b *Backend) prFiles(n int) []github.FileDiff {
	d := b.details[n]
	if d == nil {
		return nil
	}
	var out []github.FileDiff
	for _, f := range d.Files {
		status := map[string]string{"ADDED": "added", "DELETED": "removed", "RENAMED": "renamed"}[f.ChangeType]
		if status == "" {
			status = "modified"
		}
		patch := samplePatch(f.Path, f.Additions, f.Deletions)
		switch {
		case n == 4821 && strings.HasSuffix(f.Path, "registry/push_source.py"):
			patch = strings.Replace(pushSourcePy, "allow_registry_cache=True", "allow_registry_cache=False", 1)
		case n == 4821 && strings.HasSuffix(f.Path, "online_writer.py"):
			patch = onlineWriterPy
		case n == 4821 && strings.HasSuffix(f.Path, "push_source.proto"):
			patch = pushSourceProto
		}
		out = append(out, fileDiff(f.Path, status, patch))
	}
	return out
}

// PRDiff serves the sample patches of a PR's files.
func (b *Backend) PRDiff(_ context.Context, pr github.PR) ([]github.FileDiff, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.prFiles(pr.Number), nil
}

// CommitDiff serves a sample commit: each commit touches one or two of the PR's files.
func (b *Backend) CommitDiff(_ context.Context, repo, sha string) (*github.CommitDiff, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for n, d := range b.details {
		for i, c := range d.CommitList {
			if c.OID != sha {
				continue
			}
			files := b.prFiles(n)
			out := &github.CommitDiff{OID: c.OID, Message: c.Headline, Author: c.Author, At: c.At}
			if n == 4821 {
				switch i {
				case 0:
					out.Files = []github.FileDiff{files[0], fileDiff(files[1].Path, "added", pushSourcePy)}
				case 2:
					out.Files = []github.FileDiff{files[2]}
				case 4:
					out.Message += "\n\nA stale registry silently dropped new columns, so pushes now read the\nregistry fresh unless the caller opts in to the cache."
					out.Files = []github.FileDiff{fileDiff(files[1].Path, "modified", registryCacheDefault)}
				default:
					out.Files = []github.FileDiff{files[min(i, len(files)-1)]}
				}
			} else if len(files) > 0 {
				out.Files = []github.FileDiff{files[i%len(files)]}
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("no sample commit %s in %s", sha, repo)
}
