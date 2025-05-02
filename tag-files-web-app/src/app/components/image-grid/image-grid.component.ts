import {ChangeDetectionStrategy, Component, computed, inject} from '@angular/core';
import {MatGridListModule} from '@angular/material/grid-list';
import {AppStateService} from '../../services/app-state.service';
import {LightgalleryModule} from 'lightgallery/angular';
import lgZoom from 'lightgallery/plugins/zoom';
import lgRotate from 'lightgallery/plugins/rotate';
import lgVideo from 'lightgallery/plugins/video';
import {SearchService} from '../../services/search.service';
import {FileType, LibraryItem} from '../../services/api/library-api.service';

@Component({
  selector: 'app-image-grid',
  imports: [MatGridListModule, LightgalleryModule],
  templateUrl: './image-grid.component.html',
  styleUrl: './image-grid.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageGridComponent {
  protected gallerySettings = {
    plugins: [lgVideo, lgZoom, lgRotate],
    download: false,
    counter: false,
    flipHorizontal: false,
    flipVertical: false,
    rotateLeft: false
  };
  protected readonly contentBaseUrl = "http://localhost:5010/";
  protected fileTypes = FileType;
  private readonly searchService = inject(SearchService);
  protected readonly searchResults = computed(() => {
    return this.searchService.searchResults()
  });
  private readonly appStateService = inject(AppStateService);
  protected readonly getGridColumns = computed(() => {
    const max = 7;
    const level = this.appStateService.getGalleryThumbnailSize();
    return 4 + (max - level);
  })

  protected getVideoData(item: LibraryItem): string {
    return JSON.stringify({
      source: [
        {
          src: `${this.contentBaseUrl}${item.path}`,
          type: item.mediaType,
        }
      ],
      attributes: {
        preload: false,
        playsinline: true,
        controls: true
      }
    });
  }
}
