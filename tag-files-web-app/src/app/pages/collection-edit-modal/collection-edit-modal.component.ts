import {Component, inject} from '@angular/core';
import {
  MAT_DIALOG_DATA,
  MatDialog,
  MatDialogActions,
  MatDialogClose,
  MatDialogContent,
  MatDialogRef,
  MatDialogTitle,
} from '@angular/material/dialog';
import {
  CreateLibraryCollection,
  LibraryCollection,
  LibraryCollectionsApiService,
  UpdateLibraryCollection
} from '../../services/api/library-collections-api.service';
import {FormControl, FormGroup, ReactiveFormsModule, Validators} from '@angular/forms';
import {MatButton, MatIconButton} from '@angular/material/button';
import {MatInputModule} from '@angular/material/input';
import {MatFormFieldModule} from '@angular/material/form-field';
import {MatIconModule} from '@angular/material/icon';
import {ConfirmDialogComponent} from '../../components/confirm-dialog/confirm-dialog.component';

export interface CollectionEditModalData {
  collection?: LibraryCollection;
}

@Component({
  selector: 'app-collection-edit-modal',
  standalone: true,
  imports: [
    MatDialogTitle,
    MatDialogContent,
    MatDialogActions,
    MatDialogClose,
    MatButton,
    MatIconButton,
    MatInputModule,
    MatFormFieldModule,
    ReactiveFormsModule,
    MatIconModule
  ],
  templateUrl: './collection-edit-modal.component.html',
  styleUrl: './collection-edit-modal.component.scss'
})
export class CollectionEditModalComponent {
  protected readonly data: CollectionEditModalData = inject(MAT_DIALOG_DATA);
  protected readonly form = new FormGroup({
    name: new FormControl(this.data.collection?.name ?? '', [Validators.required])
  });
  private readonly dialogRef = inject(MatDialogRef<CollectionEditModalComponent>);
  private readonly collectionsService = inject(LibraryCollectionsApiService);
  private readonly dialog = inject(MatDialog);

  protected deleteClick() {
    if (!this.data.collection) {
      return;
    }

    const dialogRef = this.dialog.open(ConfirmDialogComponent, {
      data: {
        title: 'Delete Collection',
        message: `Are you sure you want to delete collection "${this.data.collection.name}"?`
      }
    });

    dialogRef.afterClosed().subscribe(result => {
      if (result === true) {
        this.collectionsService.deleteCollection(this.data.collection!.id).subscribe({
          next: () => {
            this.dialogRef.close(true);
          },
          error: (err) => {
            console.error('Error deleting collection:', err);
          }
        });
      }
    });
  }

  protected saveClick() {
    if (this.form.invalid) {
      return;
    }

    const formValue = this.form.value;
    const payload: CreateLibraryCollection | UpdateLibraryCollection = {
      name: formValue.name!
    };

    const request = this.data.collection
      ? this.collectionsService.updateCollection(this.data.collection.id, payload)
      : this.collectionsService.createCollection(payload);

    request.subscribe({
      next: () => {
        this.dialogRef.close(true);
      },
      error: (err) => {
        console.error('Error saving collection:', err);
      }
    });
  }
}
