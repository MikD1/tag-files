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
  templateUrl: './categories-table.component.html',
  styleUrl: './categories-table.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class CategoriesTableComponent {
  protected readonly categories = signal<Category[]>([]);
  protected readonly displayedColumns = ['name', 'tagQuery', 'actions'];
  private readonly categoriesService = inject(CategoriesApiService);
  private readonly searchService = inject(SearchService);
  private dialog = inject(MatDialog);
  private readonly router = inject(Router);

  constructor() {
    effect(() => {
      this.categoriesService.getCategories().subscribe((result) => {
        this.categories.set(result);
      });
    });
  }

  protected editItem(category: Category) {
    // TODO: Implement category edit modal
    console.log('Edit category:', category.id);
  }
}
