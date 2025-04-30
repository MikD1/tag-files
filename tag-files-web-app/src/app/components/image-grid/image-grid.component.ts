import {ChangeDetectionStrategy, Component, computed, inject} from '@angular/core';
import {MatGridListModule} from '@angular/material/grid-list';
import {AppStateService} from '../../services/app-state.service';
import {LightgalleryModule} from 'lightgallery/angular';
import lgZoom from 'lightgallery/plugins/zoom';
import lgRotate from 'lightgallery/plugins/rotate';
import lgVideo from 'lightgallery/plugins/video';
import {SearchService} from '../../services/search.service';

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

  isVideo(path: string): boolean {
    return path.endsWith('mp4')
  }

  getVideoData(path: string): string {
    return JSON.stringify({
      source: [
        {
          src: `http://localhost:5010/${path}`,
          type: 'video/mp4',
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
