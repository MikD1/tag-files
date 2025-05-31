using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Infrastructure;

[TestClass]
public class CategoriesRepositoryTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task SaveCategory_ShouldAddNewCategory_WhenCategoryIsNew()
    {
        CategoriesRepository repository = new(DbContext);
        Category category = new("NewCategory", "tag1 && tag2", FileType.Image);

        Category savedCategory = await repository.SaveCategory(category);
        Category? categoryFromDb = await DbContext.Categories.FindAsync(1u);

        Assert.AreEqual(1u, savedCategory.Id);
        Assert.AreEqual("NewCategory", savedCategory.Name);
        Assert.AreEqual("tag1 && tag2", savedCategory.TagQuery);
        Assert.AreEqual(FileType.Image, savedCategory.ItemsType);

        Assert.AreEqual(1u, categoryFromDb?.Id);
        Assert.AreEqual("NewCategory", categoryFromDb?.Name);
        Assert.AreEqual("tag1 && tag2", categoryFromDb?.TagQuery);
        Assert.AreEqual(FileType.Image, categoryFromDb?.ItemsType);
    }

    [TestMethod]
    public async Task SaveCategory_ShouldUpdateExistingCategory_WhenCategoryIsNotNew()
    {
        CategoriesRepository repository = new(DbContext);
        await repository.SaveCategory(new("ExistingCategory", "tag1", FileType.Image));

        Category? existingCategory = await DbContext.Categories.FindAsync(1u);
        existingCategory?.Rename("NewName");
        existingCategory?.UpdateFilter("tag2", FileType.Video);
        Category updatedCategory = await repository.SaveCategory(existingCategory!);
        Category? updatedCategoryFromDb = await DbContext.Categories.FindAsync(1u);

        Assert.AreEqual(1u, updatedCategory.Id);
        Assert.AreEqual("NewName", updatedCategory.Name);
        Assert.AreEqual("tag2", updatedCategory.TagQuery);
        Assert.AreEqual(FileType.Video, updatedCategory.ItemsType);

        Assert.AreEqual(1u, updatedCategoryFromDb?.Id);
        Assert.AreEqual("NewName", updatedCategoryFromDb?.Name);
        Assert.AreEqual("tag2", updatedCategoryFromDb?.TagQuery);
        Assert.AreEqual(FileType.Video, updatedCategoryFromDb?.ItemsType);
    }

    [TestMethod]
    public async Task GetCategories_ShouldReturnAllCategoriesOrderedByName()
    {
        DbContext.Categories.Add(new("CategoryB", null, null));
        DbContext.Categories.Add(new("999", null, null));
        DbContext.Categories.Add(new("CategoryA", null, null));
        DbContext.Categories.Add(new("123", null, null));
        DbContext.Categories.Add(new("CategoryC", null, null));
        await DbContext.SaveChangesAsync();

        CategoriesRepository repository = new(DbContext);
        List<Category> categories = await repository.GetCategories();

        Assert.AreEqual(5, categories.Count);
        Assert.AreEqual("123", categories[0].Name);
        Assert.AreEqual("999", categories[1].Name);
        Assert.AreEqual("CategoryA", categories[2].Name);
        Assert.AreEqual("CategoryB", categories[3].Name);
        Assert.AreEqual("CategoryC", categories[4].Name);
    }

    [TestMethod]
    public async Task GetCategory_ShouldReturnCategory_WhenCategoryExists()
    {
        DbContext.Categories.Add(new("Category1", "tag1", FileType.Image));
        await DbContext.SaveChangesAsync();

        CategoriesRepository repository = new(DbContext);
        Category category = await repository.GetCategory(1);

        Assert.AreEqual(1u, category.Id);
        Assert.AreEqual("Category1", category.Name);
        Assert.AreEqual("tag1", category.TagQuery);
        Assert.AreEqual(FileType.Image, category.ItemsType);
    }

    [TestMethod]
    [ExpectedException(typeof(ApplicationException))]
    public async Task GetCategory_ShouldThrowException_WhenCategoryDoesNotExist()
    {
        CategoriesRepository repository = new(DbContext);
        await repository.GetCategory(1);
    }

    [TestMethod]
    public async Task DeleteCategory_ShouldRemoveCategory_WhenCategoryExists()
    {
        DbContext.Categories.Add(new("category1", null, null));
        DbContext.Categories.Add(new("category2", null, null));
        DbContext.Categories.Add(new("category3", null, null));
        await DbContext.SaveChangesAsync();

        CategoriesRepository repository = new(DbContext);
        await repository.DeleteCategory(2);
        Category? deletedCategory = await DbContext.Categories.FindAsync(2u);
        int count = await DbContext.Categories.CountAsync();

        Assert.IsNull(deletedCategory);
        Assert.AreEqual(2, count);
    }

    [TestMethod]
    [ExpectedException(typeof(ApplicationException))]
    public async Task DeleteCategory_ShouldThrowException_WhenCategoryDoesNotExist()
    {
        CategoriesRepository repository = new(DbContext);
        await repository.DeleteCategory(1);
    }
}