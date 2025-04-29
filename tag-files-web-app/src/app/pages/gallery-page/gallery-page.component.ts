import { Component, computed, inject } from '@angular/core';
import { ImageGridComponent } from '../../components/image-grid/image-grid.component';
import { AppStateService, GalleryViewType } from '../../services/app-state.service';
import { ImageListComponent } from '../../components/image-list/image-list.component';

@Component({
  selector: 'app-gallery-page',
  imports: [ImageGridComponent, ImageListComponent],
  templateUrl: './gallery-page.component.html',
  styleUrl: './gallery-page.component.scss'
})
export class GalleryPageComponent {
  protected galleryViewTypes = GalleryViewType;

  protected readonly galleryViewType = computed<GalleryViewType>(() => {
    return this.appStateService.getGalleryViewType();
  })

  private readonly appStateService = inject(AppStateService);
}
