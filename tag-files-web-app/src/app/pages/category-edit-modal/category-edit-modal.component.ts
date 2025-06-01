import {Component, inject} from '@angular/core';
import {
  MAT_DIALOG_DATA,
  MatDialogActions,
  MatDialogClose,
  MatDialogContent,
  MatDialogRef,
  MatDialogTitle,
} from '@angular/material/dialog';
import {
  CategoriesApiService,
  Category,
  CreateCategory,
  UpdateCategory
} from '../../services/api/categories-api.service';
import {FormControl, FormGroup, ReactiveFormsModule, Validators} from '@angular/forms';
import {MatButton, MatIconButton} from '@angular/material/button';
import {MatInputModule} from '@angular/material/input';
import {MatFormFieldModule} from '@angular/material/form-field';
import {MatSelectModule} from '@angular/material/select';
import {FileType} from '../../services/api/file-type';
import {MatIconModule} from '@angular/material/icon';

export interface CategoryEditModalData {
  category?: Category;
}

@Component({
  selector: 'app-category-edit-modal',
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
    MatSelectModule,
    MatIconModule
  ],
  templateUrl: './category-edit-modal.component.html',
  styleUrl: './category-edit-modal.component.scss'
})
export class CategoryEditModalComponent {
  protected readonly data: CategoryEditModalData = inject(MAT_DIALOG_DATA);
  protected readonly fileTypes = Object.values(FileType);
  protected readonly form = new FormGroup({
    name: new FormControl(this.data.category?.name ?? '', [Validators.required]),
    tagQuery: new FormControl(this.data.category?.tagQuery ?? ''),
    itemsType: new FormControl(this.data.category?.itemsType ?? null)
  });
  private readonly dialogRef = inject(MatDialogRef<CategoryEditModalComponent>);
  private readonly categoriesService = inject(CategoriesApiService);

  protected saveClick() {
    if (this.form.invalid) {
      return;
    }

    const formValue = this.form.value;
    const payload: CreateCategory | UpdateCategory = {
      name: formValue.name ?? null,
      tagQuery: formValue.tagQuery ?? null,
      itemsType: formValue.itemsType ?? null
    };

    const request = this.data.category
      ? this.categoriesService.updateCategory(this.data.category.id, payload)
      : this.categoriesService.createCategory(payload);

    request.subscribe({
      next: () => {
        this.dialogRef.close(true);
      },
      error: (err) => {
        console.error('Error saving category:', err);
      }
    });
  }
}
