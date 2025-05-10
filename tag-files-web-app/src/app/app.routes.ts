import {Routes} from '@angular/router';
import {LibraryPageComponent} from './pages/library-page/library-page.component';
import {TagsPageComponent} from './pages/tags-page/tags-page.component';

export const routes: Routes = [
  {path: '', redirectTo: '/library', pathMatch: 'full'},
  {path: 'library', component: LibraryPageComponent},
  {path: 'tags', component: TagsPageComponent},
];
