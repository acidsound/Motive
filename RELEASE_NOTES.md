## Motive v0.5.0

Changes since v0.4.0:

### Model Picker Search (alt+m)
- `alt+m` opens the model picker with the search box already focused — just start typing a model name, no filter mode to enter
- The list narrows live as you type and ranks matches: exact id, then prefix, substring, and subsequence
- Matching ignores case and the `-`/`.`/`_` separators in model ids: `cso` finds `claude-sonnet-opus`, `35sonnet` finds `claude-3.5-sonnet`
- The best match is highlighted, so `enter` applies it directly; `↑/↓` picks another, `←/→` switches provider tabs (the query survives and re-filters the new tab)
- `esc` clears the query first, then closes the picker
- A "no model matches" message is shown instead of an empty list, and the typed query is kept while a provider tab's models are being fetched

### Tests
- Added tests for the model picker search: ranking (exact/prefix/substring/subsequence), separator- and case-insensitive matching, tab switching with a live query, and esc clear-then-close behavior
