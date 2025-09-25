import {ChangeDetectionStrategy, Component, inject} from '@angular/core';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {MatIconModule} from '@angular/material/icon';
import {
  MAT_DIALOG_DATA,
  MatDialogActions,
  MatDialogClose,
  MatDialogContent,
  MatDialogRef,
  MatDialogTitle
} from '@angular/material/dialog';
import {MatButton, MatIconButton} from '@angular/material/button';
import {DatePipe} from '@angular/common';
import {TagsFormComponent} from '../../components/tags-form/tags-form.component';
import {firstValueFrom} from 'rxjs';
import {SelectCollectionComponent} from '../../components/select-collection/select-collection.component';

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

export interface LibraryItemEditModalData {
  item: LibraryItem;
}

@Component({
  selector: 'app-library-item-edit-modal',
  imports: [
    MatIconModule,
    MatDialogTitle,
    MatDialogContent,
    MatDialogClose,
    MatIconButton,
    DatePipe,
    TagsFormComponent,
    MatDialogActions,
    MatButton,
    SelectCollectionComponent
  ],
  templateUrl: './library-item-edit-modal.component.html',
  styleUrl: './library-item-edit-modal.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class LibraryItemEditModalComponent {
  protected readonly data: LibraryItemEditModalData = inject(MAT_DIALOG_DATA);
  protected tags: string[] = this.data.item.tags;
  protected collectionId: number | null = this.data.item.collectionId;
  private readonly libraryService = inject(LibraryApiService);
  private readonly dialogRef = inject(MatDialogRef<LibraryItemEditModalComponent>);

  protected getFullThumbnailPath(): string {
    const thumbnailPath = this.data.item.thumbnailPath;
    if (!thumbnailPath) {
      return '#';
    }

    return ContentBaseUrl + thumbnailPath;
  }

  protected async saveClick() {
    const request = {
      itemsList: [this.data.item.id],
      tags: this.tags
    };

    await firstValueFrom(this.libraryService.assignTags(request));

    if (this.collectionId !== this.data.item.collectionId) {
      await firstValueFrom(this.libraryService.assignToCollection({
        itemsList: [this.data.item.id],
        collectionId: this.collectionId
      }));
    }

    this.dialogRef.close(true);
  }
}
