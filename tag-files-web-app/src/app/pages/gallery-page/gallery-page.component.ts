import {Component, computed, inject} from '@angular/core';
import {ImageGridComponent} from '../../components/image-grid/image-grid.component';
import {AppStateService, GalleryViewType} from '../../services/app-state.service';
import {ImageListComponent} from '../../components/image-list/image-list.component';
import lgVideo from 'lightgallery/plugins/video';
import lgZoom from 'lightgallery/plugins/zoom';
import lgRotate from 'lightgallery/plugins/rotate';
import {SearchService} from '../../services/search.service';

@Component({
  selector: 'app-gallery-page',
  imports: [ImageGridComponent, ImageListComponent],
  templateUrl: './gallery-page.component.html',
  styleUrl: './gallery-page.component.scss'
})
export class GalleryPageComponent {
  protected gallerySettings = {
    plugins: [lgVideo, lgZoom, lgRotate],
    selector: ".gallery-item",
    download: false,
    counter: false,
    flipHorizontal: false,
    flipVertical: false,
    rotateLeft: false
  };

  protected galleryViewTypes = GalleryViewType;
  private readonly appStateService = inject(AppStateService);
  protected readonly galleryViewType = computed<GalleryViewType>(() => {
    return this.appStateService.getGalleryViewType();
  })
  private readonly searchService = inject(SearchService);
  protected readonly searchResults = computed(() => {
    return this.searchService.searchResults()
  });
}
