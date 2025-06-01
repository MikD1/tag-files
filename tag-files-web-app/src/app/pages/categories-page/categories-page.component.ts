import {ChangeDetectionStrategy, Component, effect, inject, signal} from '@angular/core';
import {CategoriesApiService, Category} from '../../services/api/categories-api.service';
import {MatTableModule} from '@angular/material/table';
import {MatButtonModule} from '@angular/material/button';
import {MatChipsModule} from '@angular/material/chips';
import {MatListModule} from '@angular/material/list';
import {MatIconModule} from '@angular/material/icon';
import {SearchService} from '../../services/search.service';
import {MatDialog} from '@angular/material/dialog';
import {Router} from '@angular/router';
import {CategoryEditModalComponent} from '../category-edit-modal/category-edit-modal.component';

@Component({
  selector: 'app-categories-table',
  standalone: true,
  imports: [
    MatTableModule,
    MatButtonModule,
    MatChipsModule,
    MatListModule,
    MatIconModule
  ],
  templateUrl: './categories-page.component.html',
  styleUrl: './categories-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CategoriesPageComponent {
  protected readonly categories = signal<Category[]>([]);
  protected readonly displayedColumns = ['name', 'tagQuery', 'actions'];
  private readonly categoriesService = inject(CategoriesApiService);
  private readonly searchService = inject(SearchService);
  private dialog = inject(MatDialog);
  private readonly router = inject(Router);

  constructor() {
    effect(() => {
      this.loadCategories();
    });
  }

  protected editItem(category: Category) {
    const dialogRef = this.dialog.open(CategoryEditModalComponent, {
      data: {
        category: category,
      },
      width: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        this.loadCategories();
      }
    });
  }

  protected addCategory() {
    const dialogRef = this.dialog.open(CategoryEditModalComponent, {
      data: {
        category: null,
      },
      width: '600px',
    });

    dialogRef.afterClosed().subscribe((result) => {
      if (result === true) {
        this.loadCategories();
      }
    });
  }

  private loadCategories() {
    this.categoriesService.getCategories().subscribe((result) => {
      this.categories.set(result);
    });
  }
}
