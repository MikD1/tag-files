namespace TagFilesService.Model;

public class Category
{
    public Category(string name, string? tagQuery, FileType? itemType)
        : this(0u, name, tagQuery, itemType)
    {
    }

    public uint Id { get; private set; }

    public string Name { get; private set; }

    public string? TagQuery { get; private set; }

    public FileType? ItemsType { get; private set; }

    public void Rename(string newName)
    {
        ValidateName(newName);
        Name = newName;
    }

    public void UpdateFilter(string? tagQuery, FileType? itemType)
    {
        TagQuery = tagQuery;
        ItemsType = itemType;
    }

    private void ValidateName(string name)
    {
        if (name.Length is 0 or > 200)
        {
            throw new ApplicationException("Category Name cannot be empty or longer than 200 characters");
        }
    }

    private Category(uint id, string name, string? tagQuery, FileType? itemsType)
    {
        ValidateName(name);
        Id = id;
        Name = name;
        TagQuery = tagQuery;
        ItemsType = itemsType;
    }
}