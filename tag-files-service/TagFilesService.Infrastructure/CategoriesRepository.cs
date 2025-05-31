using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class CategoriesRepository(AppDbContext dbContext) : ICategoriesRepository
{
    public async Task<Category> SaveCategory(Category category)
    {
        if (category.Id == 0)
        {
            dbContext.Categories.Add(category);
        }
        else
        {
            dbContext.Categories.Update(category);
        }

        await dbContext.SaveChangesAsync();
        return category;
    }

    public async Task<List<Category>> GetCategories()
    {
        return await dbContext.Categories
            .OrderBy(x => x.Name)
            .ToListAsync();
    }

    public async Task<Category> GetCategory(uint id)
    {
        return await GetCategoryByIdOrThrow(id);
    }

    public async Task DeleteCategory(uint id)
    {
        Category category = await GetCategoryByIdOrThrow(id);
        dbContext.Categories.Remove(category);
        await dbContext.SaveChangesAsync();
    }

    private async Task<Category> GetCategoryByIdOrThrow(uint id)
    {
        Category? category = await dbContext.Categories.FirstOrDefaultAsync(x => x.Id == id);
        if (category is null)
        {
            throw new ApplicationException($"Category {id} not found");
        }

        return category;
    }
}