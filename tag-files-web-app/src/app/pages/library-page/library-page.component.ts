import {Component, computed, HostListener, inject} from '@angular/core';
import {ImageGridComponent} from '../../components/image-grid/image-grid.component';
import {AppStateService, GalleryViewType} from '../../services/app-state.service';
import {ImageListComponent} from '../../components/image-list/image-list.component';
import lgVideo from 'lightgallery/plugins/video';
import lgZoom from 'lightgallery/plugins/zoom';
import lgRotate from 'lightgallery/plugins/rotate';
import {SearchService} from '../../services/search.service';

@Component({
  selector: 'app-library-page',
  imports: [ImageGridComponent, ImageListComponent],
  templateUrl: './library-page.component.html',
  styleUrl: './library-page.component.scss'
})
export class LibraryPageComponent {
  protected gallerySettings = {
    plugins: [lgVideo, lgZoom, lgRotate],
    selector: ".gallery-item",
    download: false,
    counter: false,
    flipHorizontal: false,
    flipVertical: false,
    rotateLeft: false,
    keyPress: false,
    gotoNextSlideOnVideoEnd: false
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

  @HostListener('document:keydown', ['$event'])
  handleKeyboardEvent(event: KeyboardEvent): void {
    const videoElement = document.querySelector('video');
    if (videoElement) {
      switch (event.code) {
        case 'Space':
          if (videoElement.paused) {
            videoElement.play();
          } else {
            videoElement.pause();
          }
          break;
        case 'ArrowRight':
          videoElement.currentTime += 4;
          break;
        case 'ArrowLeft':
          videoElement.currentTime -= 2;
          break;
        case 'ArrowUp':
          videoElement.volume = Math.min(videoElement.volume + 0.1, 1);
          break;
        case 'ArrowDown':
          videoElement.volume = Math.max(videoElement.volume - 0.1, 0);
          break;
      }
    }
  }
}
