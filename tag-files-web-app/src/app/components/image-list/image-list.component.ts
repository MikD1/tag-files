import {ChangeDetectionStrategy, Component, inject, input} from '@angular/core';
import {LightgalleryModule} from 'lightgallery/angular';
import {MatTableModule} from '@angular/material/table';
import {MatChipsModule} from '@angular/material/chips';
import {LightGallerySettings} from 'lightgallery/lg-settings';
import {FileType, LibraryItem, LibraryItemPaginatedList} from '../../services/api/library-api.service';
import {MatIconModule} from '@angular/material/icon';
import {MatButtonModule} from '@angular/material/button';
import {DatePipe} from '@angular/common';
import {MatDialog} from '@angular/material/dialog';
import {LibraryItemEditModalComponent} from '../../pages/library-item-edit-modal/library-item-edit-modal.component';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-image-list',
  imports: [LightgalleryModule, MatTableModule, MatChipsModule, MatIconModule, MatButtonModule, DatePipe],
  templateUrl: './image-list.component.html',
  styleUrl: './image-list.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageListComponent {
  gallerySettings = input.required<LightGallerySettings>();
  itemsList = input.required<LibraryItemPaginatedList>();
  protected readonly fileTypes = FileType;
  protected readonly displayedColumns = ['image', 'duration', 'tags', 'uploadedOn', 'actions'];
  private dialog = inject(MatDialog);

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

  protected editItem(item: LibraryItem) {
    const dialogRef = this.dialog.open(LibraryItemEditModalComponent, {
      data: {
        item: item,
      },
      width: '800px',
      height: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        window.location.reload();
      }
    });
  }
}
