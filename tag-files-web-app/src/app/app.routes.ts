import { LibraryPageComponent } from "./pages/library-page/library-page.component";
import { TagsPageComponent } from "./pages/tags-page/tags-page.component";

import type { Routes } from "@angular/router";

export const routes: Routes = [
  { path: "", redirectTo: "/library", pathMatch: "full" },
  { path: "library", component: LibraryPageComponent },
  { path: "tags", component: TagsPageComponent },
];
