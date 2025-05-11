import { DatePipe } from "@angular/common";
import {
  ChangeDetectionStrategy,
  Component,
  inject,
  input,
} from "@angular/core";
import { MatButtonModule } from "@angular/material/button";
import { MatChipsModule } from "@angular/material/chips";
import { MatDialog } from "@angular/material/dialog";
import { MatIconModule } from "@angular/material/icon";
import { MatTableModule } from "@angular/material/table";
import { LightgalleryModule } from "lightgallery/angular";

import { LibraryItemEditModalComponent } from "../../pages/library-item-edit-modal/library-item-edit-modal.component";
import { FileType } from "../../services/api/library-api.service";

import type {
  LibraryItem,
  LibraryItemPaginatedList,
} from "../../services/api/library-api.service";
import type { LightGallerySettings } from "lightgallery/lg-settings";

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  changeDetection: ChangeDetectionStrategy.OnPush,
  selector: "app-image-list",
  imports: [
    LightgalleryModule,
    MatTableModule,
    MatChipsModule,
    MatIconModule,
    MatButtonModule,
    DatePipe,
  ],
  templateUrl: "./image-list.component.html",
  styleUrl: "./image-list.component.scss",
})
export class ImageListComponent {
  gallerySettings = input.required<LightGallerySettings>();
  itemsList = input.required<LibraryItemPaginatedList>();
  protected fileTypes = FileType;
  protected displayedColumns = [
    "image",
    "duration",
    "tags",
    "uploadedOn",
    "actions",
  ];
  private readonly dialog = inject(MatDialog);

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

  protected editItem(item: LibraryItem) {
    this.dialog.open(LibraryItemEditModalComponent, {
      data: {
        item: item,
      },
      width: "800px",
      height: "600px",
    });
  }
}
