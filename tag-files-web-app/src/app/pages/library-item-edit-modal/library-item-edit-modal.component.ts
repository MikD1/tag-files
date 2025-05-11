import { DatePipe } from "@angular/common";
import { ChangeDetectionStrategy, Component, inject } from "@angular/core";
import { MatIconButton } from "@angular/material/button";
import { MatChip, MatChipSet } from "@angular/material/chips";
import {
  MAT_DIALOG_DATA,
  MatDialogClose,
  MatDialogContent,
  MatDialogTitle,
} from "@angular/material/dialog";
import { MatIconModule } from "@angular/material/icon";

import { LibraryApiService } from "../../services/api/library-api.service";

import type { LibraryItem } from "../../services/api/library-api.service";

const ContentBaseUrl = "http://localhost:5010/"; // TODO: Move to config

export interface LibraryItemEditModalData {
  item: LibraryItem;
}

@Component({
  selector: "app-library-item-edit-modal",
  imports: [
    MatIconModule,
    MatChipSet,
    MatChip,
    MatDialogTitle,
    MatDialogContent,
    MatDialogClose,
    MatIconButton,
    DatePipe,
  ],
  templateUrl: "./library-item-edit-modal.component.html",
  styleUrl: "./library-item-edit-modal.component.scss",
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class LibraryItemEditModalComponent {
  protected readonly data: LibraryItemEditModalData = inject(MAT_DIALOG_DATA);
  private readonly libraryService = inject(LibraryApiService);

  protected getFullThumbnailPath(): string {
    const thumbnailPath = this.data.item.thumbnailPath;
    if (!thumbnailPath) {
      return "#";
    }

    return ContentBaseUrl + thumbnailPath;
  }
}
