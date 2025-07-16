namespace TagFilesService.Model;

public class LibraryCollection
{
    public LibraryCollection(string name)
        : this(0u, name)
    {
    }

    public uint Id { get; private set; }

    public string Name { get; private set; }

    public void Rename(string newName)
    {
        ValidateName(newName);
        Name = newName;
    }

    private void ValidateName(string name)
    {
        if (name.Length is 0 or > 200)
        {
            throw new ApplicationException("Category Name cannot be empty or longer than 200 characters");
        }
    }

    private LibraryCollection(uint id, string name)
    {
        ValidateName(name);
        Id = id;
        Name = name;
    }
}