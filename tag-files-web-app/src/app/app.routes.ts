import {Routes} from '@angular/router';
import {LibraryPageComponent} from './pages/library-page/library-page.component';
import {TagsPageComponent} from './pages/tags-page/tags-page.component';
import {CategoriesPageComponent} from './pages/categories-page/categories-page.component';

export const routes: Routes = [
  {path: '', redirectTo: '/library', pathMatch: 'full'},
  {path: 'library', component: LibraryPageComponent},
  {path: 'tags', component: TagsPageComponent},
  {path: 'categories', component: CategoriesPageComponent},
];
