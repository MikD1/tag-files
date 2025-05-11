import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
} from "@angular/core";
import { MatGridListModule } from "@angular/material/grid-list";
import { LightgalleryModule } from "lightgallery/angular";

import { FileType } from "../../services/api/library-api.service";
import { AppStateService } from "../../services/app-state.service";

import type {
  LibraryItem,
  LibraryItemPaginatedList,
} from "../../services/api/library-api.service";
import type { LightGallerySettings } from "lightgallery/lg-settings";

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: "app-image-grid",
  imports: [MatGridListModule, LightgalleryModule],
  templateUrl: "./image-grid.component.html",
  styleUrl: "./image-grid.component.scss",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageGridComponent {
  readonly gallerySettings = input.required<LightGallerySettings>();
  readonly itemsList = input.required<LibraryItemPaginatedList>();

  protected readonly fileTypes = FileType;
  private readonly appStateService = inject(AppStateService);
  protected readonly getGridColumns = computed(() => {
    const max = 7;
    const level = this.appStateService.getGalleryThumbnailSize();
    return 4 + (max - level);
  });

  protected getFullThumbnailPath(thumbnailPath?: string): string {
    if (!thumbnailPath) {
      return "#";
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
        },
      ],
      attributes: {
        preload: false,
        playsinline: true,
        controls: true,
      },
    });
  }
}
