import {
  AfterViewChecked,
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
  OnChanges,
  SimpleChanges
} from '@angular/core';
import {MatGridListModule} from '@angular/material/grid-list';
import {AppStateService} from '../../services/app-state.service';
import {LightgalleryModule} from 'lightgallery/angular';
import {LibraryItem, LibraryItemPaginatedList} from '../../services/api/library-api.service';
import {FileType} from '../../services/api/file-type';
import {LightGallerySettings} from 'lightgallery/lg-settings';
import {LightGallery} from 'lightgallery/lightgallery';
import {MatTooltipModule} from '@angular/material/tooltip';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-image-grid',
  imports: [MatGridListModule, LightgalleryModule, MatTooltipModule],
  templateUrl: './image-grid.component.html',
  styleUrl: './image-grid.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageGridComponent implements OnChanges, AfterViewChecked {
  gallerySettings = input.required<LightGallerySettings>();
  itemsList = input.required<LibraryItemPaginatedList>();
  protected fileTypes = FileType;
  private lightGallery?: LightGallery;
  private needRefresh = true;
  private readonly appStateService = inject(AppStateService);
  protected readonly getGridColumns = computed(() => {
    const max = 7;
    const level = this.appStateService.getGalleryThumbnailSize();
    return 4 + (max - level);
  })

  ngAfterViewChecked(): void {
    if (this.needRefresh && this.lightGallery) {
      this.lightGallery.refresh();
      this.needRefresh = false;
    }
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['itemsList'].currentValue != changes['itemsList'].previousValue) {
      this.needRefresh = true;
    }
  }

  protected onGalleryInit = (detail: any): void => {
    this.lightGallery = detail.instance;
  };

  protected getFullThumbnailPath(thumbnailPath?: string): string {
    if (!thumbnailPath) {
      return '#';
    }

    return ContentBaseUrl + thumbnailPath;
  }

  protected getFullContentPath(contentPath: string): string {
    return ContentBaseUrl + contentPath;
  }

  protected getVideoData(item: LibraryItem): string {
    return JSON.stringify({
      source: [
        {
          src: `${this.getFullContentPath(item.path)}`,
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

  protected getTagsTooltip(item: LibraryItem): string {
    if (!item.tags || item.tags.length === 0) {
      return 'No tags';
    }
    return item.tags.join(', ');
  }
}
