import { Routes } from '@angular/router';
import { GalleryPageComponent } from './pages/gallery-page/gallery-page.component';
import { TagsPageComponent } from './pages/tags-page/tags-page.component';

export const routes: Routes = [
    { path: '', redirectTo: '/gallery', pathMatch: 'full' },
    { path: 'gallery', component: GalleryPageComponent },
    { path: 'tags', component: TagsPageComponent },
];
