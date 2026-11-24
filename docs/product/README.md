# Product

Tag Files is a single-user photo and video library that runs on the owner's
machine and is used in a browser on the same machine. Media are organized with
tags, one optional collection per item, and saved tag searches called
Categories. The interface is English and always dark. Items have no titles or
file names in the UI: the content and its tags are the focus.

## Pages

| Route | Page |
|---|---|
| `/library` (`/` redirects here) | Grid or list of ready items with search, type filter and sort |
| `/content/:id` | Viewer for one item |
| `/uploads` | Queue of unfinished and failed uploads |
| `/tags` | Tags with usage statistics; create and rename |
| `/collections` | Collections with covers; create, rename, delete |
| `/categories` | Categories (saved searches); create, edit, delete |

API names differ in places: Categories are `saved-searches` (`savedSearchId`),
Photos are `kind=Image`, the viewer's `/content/:id` is `/items/{id}`, and
"same tags" is `/items/{id}/similar`.

## Library

- The URL holds the state: `q`, `photos=1`, `videos=1`, `sort`, `page`, `seed`,
  `collectionId`, `savedSearchId`. A category cannot be combined with a query or
  type filter.
- Pages hold 24 items. Only ready items are listed.
- Sorts: upload date, video duration and view count, each ascending or
  descending, and Random, which is stable for a seed; each new search gets a new
  seed.
- Grid or list view and seven density levels. Multi-select and bulk editing
  (1–500 items) exist in list view only.
- There is no favorites filter, and view counts are used for sorting only.
- Search syntax and rules: [search](search.md).

## Items

- An item has a kind (Image or Video for uploads; migrated items may also be
  Unknown, Text or Audio), tags, at most one collection, a favorite flag, a view
  count and, for video, a duration.
- Only the tag set and the collection can be edited. The editor picks existing
  tags. Saving checks the item version; a stale edit is refused and must be
  reloaded.
- Favorite is toggled in the viewer.
- Deleting an item is permanent: it disappears at once, then its media are
  removed in the background. There is no trash or undo.

## Tags

- Tags can be created and renamed; they cannot be deleted or merged.
- Names are 1–50 Unicode letters, decimal digits or hyphens. They are case
  sensitive, not normalized, and may repeat: tags are identified by ID.
- Renaming a tag keeps every category that uses it and updates their query
  text. Creating a tag makes matching unresolved category terms use it.

## Collections

- An item belongs to at most one collection. Names are 1–200 characters and may
  repeat.
- The cover is the poster of the oldest ready item in the collection.
- Deleting a collection removes it from its items and pending uploads; the
  media stay.

## Categories

- A category is a name, a tag query and an optional media kind.
- The query is stored bound to tag IDs, so tag renames keep it working. Changing
  only the name or kind keeps the binding; editing the query text binds it again.
- A query term with no matching tag stays unresolved until a tag with that name
  is created.

## Viewer

- Opening an item from a list snapshots the list order for up to 20,000 items
  for 24 hours; previous/next follow that order and skip items that are no longer
  ready.
- Images: ←/→ navigation, zoom 25–400%, display-only rotate. Video: Space play,
  → +5 s, ← −3 s, ↑/↓ volume.
- Related items: up to 4 with the same tag set and up to 4 from the same
  collection.
- An item counts one view after it stays open for 3 seconds. Each opening counts
  once, even if the request is retried.

## Uploads

- Files are chosen in the browser, optionally with a target collection; tags
  are added after upload. Only photos and video are accepted. Limits and
  conversions: [media pipeline](../architecture/media-pipeline.md).
- Row states: Checking file, Uploading, Queued, Processing, Error, Choose file,
  Cancelling. A finished upload disappears from the queue and its item appears in
  the library; there is no upload history.
- Uploads resume after a reload or restart: choosing the same file again (same
  size and SHA-256) skips parts already stored. Commands saved in the browser are
  not resent after 6 days.
- A failed upload shows its error code and stays until it is retried or
  canceled. Any upload can be canceled.

## Preferences

Stored in the browser only: grid or list view (grid by default), density 1–7
(default 4), and whether the sidebar is open.

## Status

`/api/v1/system/status` reports versions, library ID, job counts and disk
capacity; no page shows it. Health endpoints are listed in
[operations](../operations/README.md#monitoring-and-logs).
