import {ChangeDetectionStrategy, Component, inject} from '@angular/core';
import {LibraryApiService, LibraryItem} from '../../services/api/library-api.service';
import {MatChip, MatChipSet} from '@angular/material/chips';
import {MatIconModule} from '@angular/material/icon';
import {MAT_DIALOG_DATA, MatDialogClose, MatDialogContent, MatDialogTitle} from '@angular/material/dialog';
import {MatIconButton} from '@angular/material/button';


const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

export interface LibraryItemEditModalData {
  item: LibraryItem;
}

@Component({
  selector: 'app-library-item-edit-modal',
  imports: [
    MatIconModule,
    MatChipSet,
    MatChip,
    MatDialogTitle,
    MatDialogContent,
    MatDialogClose,
    MatIconButton
  ],
  templateUrl: './library-item-edit-modal.component.html',
  styleUrl: './library-item-edit-modal.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class LibraryItemEditModalComponent {
  protected data: LibraryItemEditModalData = inject(MAT_DIALOG_DATA);
  private readonly libraryService = inject(LibraryApiService);

  protected getFullThumbnailPath(): string {
    const thumbnailPath = this.data.item.thumbnailPath;
    if (!thumbnailPath) {
      return '#';
    }

    return ContentBaseUrl + thumbnailPath;
  }
}
