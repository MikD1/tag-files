import {
  AfterViewChecked,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  inject,
  input,
  OnChanges,
  OnInit,
  SimpleChanges
} from '@angular/core';
import {LightgalleryModule} from 'lightgallery/angular';
import {MatTableModule} from '@angular/material/table';
import {MatChipsModule} from '@angular/material/chips';
import {LightGallerySettings} from 'lightgallery/lg-settings';
import {LibraryItem, LibraryItemPaginatedList} from '../../services/api/library-api.service';
import {MatIconModule} from '@angular/material/icon';
import {MatButtonModule} from '@angular/material/button';
import {DatePipe} from '@angular/common';
import {MatDialog} from '@angular/material/dialog';
import {LibraryItemEditModalComponent} from '../../pages/library-item-edit-modal/library-item-edit-modal.component';
import {LightGallery} from 'lightgallery/lightgallery';
import {FileType} from '../../services/api/file-type';
import {LibraryCollectionsApiService, LibraryCollection} from '../../services/api/library-collections-api.service';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

@Component({
  selector: 'app-image-list',
  imports: [LightgalleryModule, MatTableModule, MatChipsModule, MatIconModule, MatButtonModule, DatePipe],
  templateUrl: './image-list.component.html',
  styleUrl: './image-list.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ImageListComponent implements OnChanges, OnInit, AfterViewChecked {
  gallerySettings = input.required<LightGallerySettings>();
  itemsList = input.required<LibraryItemPaginatedList>();
  protected readonly fileTypes = FileType;
  protected readonly displayedColumns = ['image', 'duration', 'tags', 'collection', 'uploadedOn', 'actions'];
  private lightGallery?: LightGallery;
  private needRefresh = true;
  private dialog = inject(MatDialog);
  private readonly collectionsApi = inject(LibraryCollectionsApiService);
  private readonly changeDetectorRef = inject(ChangeDetectorRef);
  private collectionIdToName = new Map<number, string>();

  ngOnInit(): void {
    this.collectionsApi.getCollections().subscribe((collections: LibraryCollection[]) => {
      for (const collection of collections) {
        this.collectionIdToName.set(collection.id, collection.name);
      }
      this.changeDetectorRef.markForCheck();
    });
  }

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
          type: "video/mp4",
        }
      ],
      attributes: {
        preload: false,
        playsinline: true,
        controls: true
      }
    });
  }

  protected getCollectionName(collectionId?: number): string {
    if (!collectionId) {
      return '';
    }
    return this.collectionIdToName.get(collectionId) ?? '';
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
