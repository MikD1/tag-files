import {Component, inject} from '@angular/core';
import {
  MAT_DIALOG_DATA,
  MatDialogActions,
  MatDialogClose,
  MatDialogContent,
  MatDialogRef,
  MatDialogTitle,
} from '@angular/material/dialog';
import {TagsApiService} from '../../services/api/tags-api.service';
import {FormControl, ReactiveFormsModule} from '@angular/forms';
import {MatButton} from '@angular/material/button';
import {MatInputModule} from '@angular/material/input';
import {MatFormFieldModule} from '@angular/material/form-field';

export interface TagEditModalData {
  tag?: string;
}

@Component({
  selector: 'app-tag-edit-modal',
  imports: [
    MatButton,
    MatDialogActions,
    MatDialogContent,
    MatDialogTitle,
    MatDialogClose,
    ReactiveFormsModule,
    MatInputModule,
    MatFormFieldModule
  ],
  templateUrl: './tag-edit-modal.component.html',
  styleUrl: './tag-edit-modal.component.scss'
})
export class TagEditModalComponent {
  readonly tagName = new FormControl<string>('');
  protected readonly data: TagEditModalData = inject(MAT_DIALOG_DATA);
  private readonly tagsService = inject(TagsApiService);
  private readonly dialogRef = inject(MatDialogRef<TagEditModalComponent>);

  constructor() {
    this.tagName.setValue(this.data.tag ?? '');
  }

  protected saveClick() {
    if (!this.tagName.value) {
      return;
    }

    if (this.data.tag) {
      this.tagsService.updateTag(this.data.tag, this.tagName.value).subscribe({
        next: () => {
          this.dialogRef.close(true);
        },
        error: (err) => {
          console.error('Error creating tag:', err);
        },
      });
    } else if (this.tagName.value) {
      this.tagsService.createTag(this.tagName.value).subscribe({
        next: () => {
          this.dialogRef.close(true);
        },
        error: (err) => {
          console.error('Error saving tag:', err);
        },
      });
    }
  }
}
